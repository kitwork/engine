package hydrate

import "math"

// The transcendental functions of Math, which JavaScript lets each engine approximate. V8 computes them
// with fdlibm (Sun's freely distributable math library), ported in its src/base/ieee754.cc; Go's math
// package computes most of them otherwise (Cephes, or assembly on amd64), and a result differed from
// the browser's in its last bit in up to a third of the cases (16/10/2026: Math.sin(2) was
// 0.9092974268256816 here and 0.9092974268256817 in node). These are the fdlibm algorithms, constant
// for constant, as V8 has them; every product added to something is converted to float64 first, so a
// compiler that fuses a multiply and an add (arm64) cannot round it differently from V8.
//
// atanh, expm1, log1p and sqrt are Go's: they are fdlibm's already and agree with node bit for bit.

func highWord(x float64) int32 { return int32(math.Float64bits(x) >> 32) }
func lowWord(x float64) uint32 { return uint32(math.Float64bits(x)) }
func withLowWord(x float64, lo uint32) float64 {
	return math.Float64frombits(math.Float64bits(x)&0xFFFFFFFF00000000 | uint64(lo))
}
func withHighWord(x float64, hi int32) float64 {
	return math.Float64frombits(uint64(uint32(hi))<<32 | math.Float64bits(x)&0xFFFFFFFF)
}
func fromWords(hi int32, lo uint32) float64 {
	return math.Float64frombits(uint64(uint32(hi))<<32 | uint64(lo))
}

func jsAtanh(x float64) float64 { return math.Atanh(x) }
func jsExpm1(x float64) float64 { return math.Expm1(x) }
func jsLog1p(x float64) float64 { return math.Log1p(x) }

// ---- argument reduction: x = n·(π/2) + y[0] + y[1] ----

var twoOverPi = [...]int32{
	0xA2F983, 0x6E4E44, 0x1529FC, 0x2757D1, 0xF534DD, 0xC0DB62, 0x95993C,
	0x439041, 0xFE5163, 0xABDEBB, 0xC561B7, 0x246E3A, 0x424DD2, 0xE00649,
	0x2EEA09, 0xD1921C, 0xFE1DEB, 0x1CB129, 0xA73EE8, 0x8235F5, 0x2EBB44,
	0x84E99C, 0x7026B4, 0x5F7E41, 0x3991D6, 0x398353, 0x39F49C, 0x845F8B,
	0xBDF928, 0x3B1FF8, 0x97FFDE, 0x05980F, 0xEF2F11, 0x8B5A0A, 0x6D1F6D,
	0x367ECF, 0x27CB09, 0xB74F46, 0x3F669E, 0x5FEA2D, 0x7527BA, 0xC7EBE5,
	0xF17B3D, 0x0739F7, 0x8A5292, 0xEA6BFB, 0x5FB11F, 0x8D5D08, 0x560330,
	0x46FC7B, 0x6BABF0, 0xCFBC20, 0x9AF436, 0x1DA9E3, 0x91615E, 0xE61B08,
	0x659985, 0x5F14A0, 0x68408D, 0xFFD880, 0x4D7327, 0x310606, 0x1556CA,
	0x73A8C9, 0x60E27B, 0xC08C6B,
}

var nPio2HighWord = [...]int32{
	0x3FF921FB, 0x400921FB, 0x4012D97C, 0x401921FB, 0x401F6A7A, 0x4022D97C,
	0x4025FDBB, 0x402921FB, 0x402C463A, 0x402F6A7A, 0x4031475C, 0x4032D97C,
	0x40346B9C, 0x4035FDBB, 0x40378FDB, 0x403921FB, 0x403AB41B, 0x403C463A,
	0x403DD85A, 0x403F6A7A, 0x40407E4C, 0x4041475C, 0x4042106C, 0x4042D97C,
	0x4043A28C, 0x40446B9C, 0x404534AC, 0x4045FDBB, 0x4046C6CB, 0x40478FDB,
	0x404858EB, 0x404921FB,
}

const (
	two24   = 1.67772160000000000000e+07
	twon24  = 5.96046447753906250000e-08
	invpio2 = 6.36619772367581382433e-01
	pio2_1  = 1.57079632673412561417e+00
	pio2_1t = 6.07710050650619224932e-11
	pio2_2  = 6.07710050630396597660e-11
	pio2_2t = 2.02226624879595063154e-21
	pio2_3  = 2.02226624871116645580e-21
	pio2_3t = 8.47842766036889956997e-32
)

// remPio2 is fdlibm's __ieee754_rem_pio2.
func remPio2(x float64) (int32, float64, float64) {
	hx := highWord(x)
	ix := hx & 0x7fffffff
	if ix <= 0x3fe921fb {
		return 0, x, 0
	}
	if ix < 0x4002d97c {
		if hx > 0 {
			z := x - pio2_1
			if ix != 0x3ff921fb {
				y0 := z - pio2_1t
				return 1, y0, (z - y0) - pio2_1t
			}
			z -= pio2_2
			y0 := z - pio2_2t
			return 1, y0, (z - y0) - pio2_2t
		}
		z := x + pio2_1
		if ix != 0x3ff921fb {
			y0 := z + pio2_1t
			return -1, y0, (z - y0) + pio2_1t
		}
		z += pio2_2
		y0 := z + pio2_2t
		return -1, y0, (z - y0) + pio2_2t
	}
	if ix <= 0x413921fb {
		t := math.Abs(x)
		n := int32(float64(t*invpio2) + 0.5)
		fn := float64(n)
		r := t - float64(fn*pio2_1)
		w := fn * pio2_1t
		var y0 float64
		if n < 32 && ix != nPio2HighWord[n-1] {
			y0 = r - w
		} else {
			j := ix >> 20
			y0 = r - w
			i := j - ((highWord(y0) >> 20) & 0x7ff)
			if i > 16 {
				t = r
				w = fn * pio2_2
				r = t - w
				w = float64(fn*pio2_2t) - ((t - r) - w)
				y0 = r - w
				i = j - ((highWord(y0) >> 20) & 0x7ff)
				if i > 49 {
					t = r
					w = fn * pio2_3
					r = t - w
					w = float64(fn*pio2_3t) - ((t - r) - w)
					y0 = r - w
				}
			}
		}
		y1 := (r - y0) - w
		if hx < 0 {
			return -n, -y0, -y1
		}
		return n, y0, y1
	}
	if ix >= 0x7ff00000 {
		return 0, x - x, x - x
	}
	z := withLowWord(0, lowWord(x))
	e0 := (ix >> 20) - 1046
	z = withHighWord(z, ix-int32(uint32(e0)<<20))
	var tx [3]float64
	for i := 0; i < 2; i++ {
		tx[i] = float64(int32(z))
		z = (z - tx[i]) * two24
	}
	tx[2] = z
	nx := 3
	for tx[nx-1] == 0 {
		nx--
	}
	var y [3]float64
	n := kernelRemPio2(tx[:nx], y[:], int(e0), nx, 2)
	if hx < 0 {
		return int32(-n), -y[0], -y[1]
	}
	return int32(n), y[0], y[1]
}

var piO2Table = [...]float64{
	1.57079625129699707031e+00,
	7.54978941586159635335e-08,
	5.39030252995776476554e-15,
	3.28200341580791294123e-22,
	1.27065575308067607349e-29,
	1.22933308981111328932e-36,
	2.73370053816464559624e-44,
	2.16741683877804819444e-51,
}

// kernelRemPio2 is fdlibm's __kernel_rem_pio2 for the precision of a double (prec 2).
func kernelRemPio2(x []float64, y []float64, e0, nx, prec int) int {
	initJk := [...]int{2, 3, 4, 6}
	var iq [20]int32
	var f, fq, q [20]float64
	jk := initJk[prec]
	jp := jk
	jx := nx - 1
	jv := (e0 - 3) / 24
	if jv < 0 {
		jv = 0
	}
	q0 := e0 - 24*(jv+1)
	j := jv - jx
	m := jx + jk
	for i := 0; i <= m; i, j = i+1, j+1 {
		if j < 0 {
			f[i] = 0
		} else {
			f[i] = float64(twoOverPi[j])
		}
	}
	for i := 0; i <= jk; i++ {
		fw := 0.0
		for j := 0; j <= jx; j++ {
			fw += float64(x[j] * f[jx+i-j])
		}
		q[i] = fw
	}
	jz := jk
	var z, fw float64
	var n, ih int32
	for {
		i := 0
		j = jz
		z = q[jz]
		for ; j > 0; i, j = i+1, j-1 {
			fw = float64(int32(twon24 * z))
			iq[i] = int32(z - float64(two24*fw))
			z = q[j-1] + fw
		}
		z = math.Ldexp(z, q0)
		z -= float64(8.0 * math.Floor(z*0.125))
		n = int32(z)
		z -= float64(n)
		ih = 0
		if q0 > 0 {
			i := iq[jz-1] >> (24 - q0)
			n += i
			iq[jz-1] -= i << (24 - q0)
			ih = iq[jz-1] >> (23 - q0)
		} else if q0 == 0 {
			ih = iq[jz-1] >> 23
		} else if z >= 0.5 {
			ih = 2
		}
		if ih > 0 {
			n++
			carry := int32(0)
			for i := 0; i < jz; i++ {
				j := iq[i]
				if carry == 0 {
					if j != 0 {
						carry = 1
						iq[i] = 0x1000000 - j
					}
				} else {
					iq[i] = 0xffffff - j
				}
			}
			if q0 > 0 {
				switch q0 {
				case 1:
					iq[jz-1] &= 0x7fffff
				case 2:
					iq[jz-1] &= 0x3fffff
				}
			}
			if ih == 2 {
				z = 1 - z
				if carry != 0 {
					z -= math.Ldexp(1, q0)
				}
			}
		}
		if z == 0 {
			j := int32(0)
			for i := jz - 1; i >= jk; i-- {
				j |= iq[i]
			}
			if j == 0 {
				k := 1
				for ; jk >= k && iq[jk-k] == 0; k++ {
				}
				for i := jz + 1; i <= jz+k; i++ {
					f[jx+i] = float64(twoOverPi[jv+i])
					fw := 0.0
					for j := 0; j <= jx; j++ {
						fw += float64(x[j] * f[jx+i-j])
					}
					q[i] = fw
				}
				jz += k
				continue
			}
		}
		break
	}
	if z == 0 {
		jz--
		q0 -= 24
		for iq[jz] == 0 {
			jz--
			q0 -= 24
		}
	} else {
		z = math.Ldexp(z, -q0)
		if z >= two24 {
			fw = float64(int32(twon24 * z))
			iq[jz] = int32(z - float64(two24*fw))
			jz++
			q0 += 24
			iq[jz] = int32(fw)
		} else {
			iq[jz] = int32(z)
		}
	}
	fw = math.Ldexp(1, q0)
	for i := jz; i >= 0; i-- {
		q[i] = fw * float64(iq[i])
		fw *= twon24
	}
	for i := jz; i >= 0; i-- {
		fw = 0.0
		for k := 0; k <= jp && k <= jz-i; k++ {
			fw += float64(piO2Table[k] * q[i+k])
		}
		fq[jz-i] = fw
	}
	fw = 0.0
	for i := jz; i >= 0; i-- {
		fw += fq[i]
	}
	if ih == 0 {
		y[0] = fw
	} else {
		y[0] = -fw
	}
	fw = fq[0] - fw
	for i := 1; i <= jz; i++ {
		fw += fq[i]
	}
	if ih == 0 {
		y[1] = fw
	} else {
		y[1] = -fw
	}
	return int(n & 7)
}

// ---- the kernels on [-π/4, π/4] ----

const (
	kS1 = -1.66666666666666324348e-01
	kS2 = 8.33333333332248946124e-03
	kS3 = -1.98412698298579493134e-04
	kS4 = 2.75573137070700676789e-06
	kS5 = -2.50507602534068634195e-08
	kS6 = 1.58969099521155010221e-10
)

func kernelSin(x, y float64, iy int) float64 {
	ix := highWord(x) & 0x7fffffff
	if ix < 0x3e400000 {
		if int(x) == 0 {
			return x
		}
	}
	z := x * x
	v := z * x
	r := kS2 + float64(z*(kS3+float64(z*(kS4+float64(z*(kS5+float64(z*kS6)))))))
	if iy == 0 {
		return x + float64(v*(kS1+float64(z*r)))
	}
	return x - ((float64(z*(float64(0.5*y)-float64(v*r))) - y) - float64(v*kS1))
}

const (
	kC1 = 4.16666666666666019037e-02
	kC2 = -1.38888888888741095749e-03
	kC3 = 2.48015872894767294178e-05
	kC4 = -2.75573143513906633035e-07
	kC5 = 2.08757232129817482790e-09
	kC6 = -1.13596475577881948265e-11
)

func kernelCos(x, y float64) float64 {
	ix := highWord(x) & 0x7fffffff
	if ix < 0x3e400000 {
		if int(x) == 0 {
			return 1
		}
	}
	z := x * x
	r := z * (kC1 + float64(z*(kC2+float64(z*(kC3+float64(z*(kC4+float64(z*(kC5+float64(z*kC6))))))))))
	if ix < 0x3FD33333 {
		return 1 - (float64(0.5*z) - (float64(z*r) - float64(x*y)))
	}
	var qx float64
	if ix > 0x3fe90000 {
		qx = 0.28125
	} else {
		qx = fromWords(ix-0x00200000, 0)
	}
	iz := float64(0.5*z) - qx
	a := 1 - qx
	return a - (iz - (float64(z*r) - float64(x*y)))
}

var kT = [...]float64{
	3.33333333333334091986e-01,
	1.33333333333201242699e-01,
	5.39682539762260521377e-02,
	2.18694882948595424599e-02,
	8.86323982359930005737e-03,
	3.59207910759131235356e-03,
	1.45620945432529025516e-03,
	5.88041240820264096874e-04,
	2.46463134818469906812e-04,
	7.81794442939557092300e-05,
	7.14072491382608190305e-05,
	-1.85586374855275456654e-05,
	2.59073051863633712884e-05,
}

const (
	kPio4   = 7.85398163397448278999e-01
	kPio4lo = 3.06161699786838301793e-17
)

func kernelTan(x, y float64, iy int) float64 {
	hx := highWord(x)
	ix := hx & 0x7fffffff
	if ix < 0x3e300000 {
		if int(x) == 0 {
			if (uint32(ix)|lowWord(x))|uint32(iy+1) == 0 {
				return 1 / math.Abs(x)
			}
			if iy == 1 {
				return x
			}
			w := x + y
			z := withLowWord(w, 0)
			v := y - (z - x)
			a := -1 / w
			t := withLowWord(a, 0)
			s := 1 + float64(t*z)
			return t + float64(a*(s+float64(t*v)))
		}
	}
	if ix >= 0x3FE59428 {
		if hx < 0 {
			x = -x
			y = -y
		}
		z := kPio4 - x
		w := kPio4lo - y
		x = z + w
		y = 0.0
	}
	z := x * x
	w := z * z
	r := kT[1] + float64(w*(kT[3]+float64(w*(kT[5]+float64(w*(kT[7]+float64(w*(kT[9]+float64(w*kT[11])))))))))
	v := z * (kT[2] + float64(w*(kT[4]+float64(w*(kT[6]+float64(w*(kT[8]+float64(w*(kT[10]+float64(w*kT[12]))))))))))
	s := z * x
	r = y + float64(z*(float64(s*(r+v))+y))
	r += float64(kT[0] * s)
	w = x + r
	if ix >= 0x3FE59428 {
		v = float64(iy)
		return float64(1-((hx>>30)&2)) * (v - float64(2.0*(x-(float64(w*w)/(w+v)-r))))
	}
	if iy == 1 {
		return w
	}
	z = withLowWord(w, 0)
	v = r - (z - x)
	a := -1.0 / w
	t := withLowWord(a, 0)
	s = 1.0 + float64(t*z)
	return t + float64(a*(s+float64(t*v)))
}

func jsSin(x float64) float64 {
	ix := highWord(x) & 0x7fffffff
	if ix <= 0x3fe921fb {
		return kernelSin(x, 0, 0)
	}
	if ix >= 0x7ff00000 {
		return x - x
	}
	n, y0, y1 := remPio2(x)
	switch n & 3 {
	case 0:
		return kernelSin(y0, y1, 1)
	case 1:
		return kernelCos(y0, y1)
	case 2:
		return -kernelSin(y0, y1, 1)
	}
	return -kernelCos(y0, y1)
}

func jsCos(x float64) float64 {
	ix := highWord(x) & 0x7fffffff
	if ix <= 0x3fe921fb {
		return kernelCos(x, 0)
	}
	if ix >= 0x7ff00000 {
		return x - x
	}
	n, y0, y1 := remPio2(x)
	switch n & 3 {
	case 0:
		return kernelCos(y0, y1)
	case 1:
		return -kernelSin(y0, y1, 1)
	case 2:
		return -kernelCos(y0, y1)
	}
	return kernelSin(y0, y1, 1)
}

func jsTan(x float64) float64 {
	ix := highWord(x) & 0x7fffffff
	if ix <= 0x3fe921fb {
		return kernelTan(x, 0, 1)
	}
	if ix >= 0x7ff00000 {
		return x - x
	}
	n, y0, y1 := remPio2(x)
	return kernelTan(y0, y1, int(1-((n&1)<<1)))
}

// ---- inverse trigonometric ----

var atanHi = [...]float64{4.63647609000806093515e-01, 7.85398163397448278999e-01, 9.82793723247329054082e-01, 1.57079632679489655800e+00}
var atanLo = [...]float64{2.26987774529616870924e-17, 3.06161699786838301793e-17, 1.39033110312309984516e-17, 6.12323399573676603587e-17}
var aT = [...]float64{
	3.33333333333329318027e-01,
	-1.99999999998764832476e-01,
	1.42857142725034663711e-01,
	-1.11111104054623557880e-01,
	9.09088713343650656196e-02,
	-7.69187620504482999495e-02,
	6.66107313738753120669e-02,
	-5.83357013379057348645e-02,
	4.97687799461593236017e-02,
	-3.65315727442169155270e-02,
	1.62858201153657823623e-02,
}

func jsAtan(x float64) float64 {
	hx := highWord(x)
	ix := hx & 0x7fffffff
	var id int
	if ix >= 0x44100000 {
		if x != x {
			return x + x
		}
		if hx > 0 {
			return atanHi[3] + atanLo[3]
		}
		return -atanHi[3] - atanLo[3]
	}
	if ix < 0x3fdc0000 {
		if ix < 0x3e400000 {
			return x
		}
		id = -1
	} else {
		x = math.Abs(x)
		if ix < 0x3ff30000 {
			if ix < 0x3fe60000 {
				id = 0
				x = (float64(2.0*x) - 1) / (2.0 + x)
			} else {
				id = 1
				x = (x - 1) / (x + 1)
			}
		} else {
			if ix < 0x40038000 {
				id = 2
				x = (x - 1.5) / (1 + float64(1.5*x))
			} else {
				id = 3
				x = -1.0 / x
			}
		}
	}
	z := x * x
	w := z * z
	s1 := z * (aT[0] + float64(w*(aT[2]+float64(w*(aT[4]+float64(w*(aT[6]+float64(w*(aT[8]+float64(w*aT[10]))))))))))
	s2 := w * (aT[1] + float64(w*(aT[3]+float64(w*(aT[5]+float64(w*(aT[7]+float64(w*aT[9]))))))))
	if id < 0 {
		return x - float64(x*(s1+s2))
	}
	z = atanHi[id] - ((float64(x*(s1+s2)) - atanLo[id]) - x)
	if hx < 0 {
		return -z
	}
	return z
}

func jsAtan2(y, x float64) float64 {
	const (
		tiny  = 1.0e-300
		piO4  = 7.8539816339744827900e-01
		piO2  = 1.5707963267948965580e+00
		pi    = 3.1415926535897931160e+00
		piLow = 1.2246467991473531772e-16
	)
	if x != x || y != y {
		return x + y
	}
	hx, lx := highWord(x), lowWord(x)
	ix := hx & 0x7fffffff
	hy, ly := highWord(y), lowWord(y)
	iy := hy & 0x7fffffff
	if x == 1.0 {
		return jsAtan(y)
	}
	m := ((hy >> 31) & 1) | ((hx >> 30) & 2)
	if uint32(iy)|ly == 0 {
		switch m {
		case 0, 1:
			return y
		case 2:
			return pi + tiny
		}
		return -pi - tiny
	}
	if uint32(ix)|lx == 0 {
		if hy < 0 {
			return -piO2 - tiny
		}
		return piO2 + tiny
	}
	if ix == 0x7ff00000 {
		if iy == 0x7ff00000 {
			switch m {
			case 0:
				return piO4 + tiny
			case 1:
				return -piO4 - tiny
			case 2:
				return float64(3.0*piO4) + tiny
			}
			return float64(-3.0*piO4) - tiny
		}
		switch m {
		case 0:
			return 0
		case 1:
			return math.Copysign(0, -1)
		case 2:
			return pi + tiny
		}
		return -pi - tiny
	}
	if iy == 0x7ff00000 {
		if hy < 0 {
			return -piO2 - tiny
		}
		return piO2 + tiny
	}
	k := (iy - ix) >> 20
	var z float64
	switch {
	case k > 60:
		z = piO2 + float64(0.5*piLow)
		m &= 1
	case hx < 0 && k < -60:
		z = 0.0
	default:
		z = jsAtan(math.Abs(y / x))
	}
	switch m {
	case 0:
		return z
	case 1:
		return -z
	case 2:
		return pi - (z - piLow)
	}
	return (z - piLow) - pi
}

const (
	pS0    = 1.66666666666666657415e-01
	pS1    = -3.25565818622400915405e-01
	pS2    = 2.01212532134862925881e-01
	pS3    = -4.00555345006794114027e-02
	pS4    = 7.91534994289814532176e-04
	pS5    = 3.47933107596021167570e-05
	qS1    = -2.40339491173441421878e+00
	qS2    = 2.02094576023350569471e+00
	qS3    = -6.88283971605453293030e-01
	qS4    = 7.70381505559019352791e-02
	pio2Hi = 1.57079632679489655800e+00
	pio2Lo = 6.12323399573676603587e-17
	pio4Hi = 7.85398163397448278999e-01
)

func asinP(t float64) float64 {
	return t * (pS0 + float64(t*(pS1+float64(t*(pS2+float64(t*(pS3+float64(t*(pS4+float64(t*pS5))))))))))
}

func asinQ(t float64) float64 {
	return 1 + float64(t*(qS1+float64(t*(qS2+float64(t*(qS3+float64(t*qS4)))))))
}

func jsAsin(x float64) float64 {
	hx := highWord(x)
	ix := hx & 0x7fffffff
	if ix >= 0x3ff00000 {
		if uint32(ix-0x3ff00000)|lowWord(x) == 0 {
			return float64(x*pio2Hi) + float64(x*pio2Lo)
		}
		return (x - x) / (x - x)
	}
	if ix < 0x3fe00000 {
		if ix < 0x3e400000 {
			return x
		}
		t := x * x
		w := asinP(t) / asinQ(t)
		return x + float64(x*w)
	}
	w := 1 - math.Abs(x)
	t := w * 0.5
	p := asinP(t)
	q := asinQ(t)
	s := math.Sqrt(t)
	if ix >= 0x3FEF3333 {
		w = p / q
		t = pio2Hi - (float64(2.0*(s+float64(s*w))) - pio2Lo)
	} else {
		w = withLowWord(s, 0)
		c := (t - float64(w*w)) / (s + w)
		r := p / q
		p = float64(float64(2.0*s)*r) - (pio2Lo - float64(2.0*c))
		q = pio4Hi - float64(2.0*w)
		t = pio4Hi - (p - q)
	}
	if hx > 0 {
		return t
	}
	return -t
}

func jsAcos(x float64) float64 {
	const pi = 3.14159265358979311600e+00
	hx := highWord(x)
	ix := hx & 0x7fffffff
	if ix >= 0x3ff00000 {
		if uint32(ix-0x3ff00000)|lowWord(x) == 0 {
			if hx > 0 {
				return 0.0
			}
			return pi + float64(2.0*pio2Lo)
		}
		return (x - x) / (x - x)
	}
	if ix < 0x3fe00000 {
		if ix <= 0x3c600000 {
			return pio2Hi + pio2Lo
		}
		z := x * x
		r := asinP(z) / asinQ(z)
		return pio2Hi - (x - (pio2Lo - float64(x*r)))
	}
	if hx < 0 {
		z := (1 + x) * 0.5
		p := asinP(z)
		q := asinQ(z)
		s := math.Sqrt(z)
		r := p / q
		w := float64(r*s) - pio2Lo
		return pi - float64(2.0*(s+w))
	}
	z := (1 - x) * 0.5
	s := math.Sqrt(z)
	df := withLowWord(s, 0)
	c := (z - float64(df*df)) / (s + df)
	p := asinP(z)
	q := asinQ(z)
	r := p / q
	w := float64(r*s) + c
	return 2.0 * (df + w)
}

const ln2 = 6.93147180559945286227e-01

func jsAcosh(x float64) float64 {
	hx, lx := highWord(x), lowWord(x)
	switch {
	case hx < 0x3ff00000:
		return (x - x) / (x - x)
	case hx >= 0x41b00000:
		if hx >= 0x7ff00000 {
			return x + x
		}
		return jsLog(x) + ln2
	case uint32(hx-0x3ff00000)|lx == 0:
		return 0.0
	case hx > 0x40000000:
		t := x * x
		return jsLog(float64(2.0*x) - 1/(x+math.Sqrt(t-1)))
	}
	t := x - 1
	return jsLog1p(t + math.Sqrt(float64(2.0*t)+float64(t*t)))
}

func jsAsinh(x float64) float64 {
	hx := highWord(x)
	ix := hx & 0x7fffffff
	if ix >= 0x7ff00000 {
		return x + x
	}
	if ix < 0x3e300000 {
		return x
	}
	var w float64
	switch {
	case ix > 0x41b00000:
		w = jsLog(math.Abs(x)) + ln2
	case ix > 0x40000000:
		t := math.Abs(x)
		w = jsLog(float64(2.0*t) + 1/(math.Sqrt(float64(x*x)+1)+t))
	default:
		t := x * x
		w = jsLog1p(math.Abs(x) + t/(1+math.Sqrt(1+t)))
	}
	if hx > 0 {
		return w
	}
	return -w
}

// ---- exponential and logarithm ----

func jsExp(x float64) float64 {
	const (
		oThreshold = 7.09782712893383973096e+02
		uThreshold = -7.45133219101941108420e+02
		invln2     = 1.44269504088896338700e+00
		eP1        = 1.66666666666666019037e-01
		eP2        = -2.77777777770155933842e-03
		eP3        = 6.61375632143793436117e-05
		eP4        = -1.65339022054652515390e-06
		eP5        = 4.13813679705723846039e-08
		huge       = 1.0e+300
		twom1000   = 9.33263618503218878990e-302
		two1023    = 8.988465674311579539e307
	)
	ln2HI := [2]float64{6.93147180369123816490e-01, -6.93147180369123816490e-01}
	ln2LO := [2]float64{1.90821492927058770002e-10, -1.90821492927058770002e-10}
	halF := [2]float64{0.5, -0.5}
	hx := uint32(highWord(x))
	xsb := int((hx >> 31) & 1)
	hx &= 0x7fffffff
	var hi, lo float64
	k := 0
	if hx >= 0x40862E42 {
		if hx >= 0x7ff00000 {
			if (hx&0xfffff)|lowWord(x) != 0 {
				return x + x
			}
			if xsb == 0 {
				return x
			}
			return 0.0
		}
		if x > oThreshold {
			return math.Inf(1)
		}
		if x < uThreshold {
			return 0
		}
	}
	if hx > 0x3fd62e42 {
		if hx < 0x3FF0A2B2 {
			if x == 1.0 {
				return math.E
			}
			hi = x - ln2HI[xsb]
			lo = ln2LO[xsb]
			k = 1 - xsb - xsb
		} else {
			k = int(float64(invln2*x) + halF[xsb])
			t := float64(k)
			hi = x - float64(t*ln2HI[0])
			lo = t * ln2LO[0]
		}
		x = hi - lo
	} else if hx < 0x3e300000 {
		return 1 + x
	} else {
		k = 0
	}
	t := x * x
	var twopk float64
	if k >= -1021 {
		twopk = fromWords(int32(0x3ff00000+(k<<20)), 0)
	} else {
		twopk = fromWords(int32(0x3ff00000+((k+1000)<<20)), 0)
	}
	c := x - float64(t*(eP1+float64(t*(eP2+float64(t*(eP3+float64(t*(eP4+float64(t*eP5)))))))))
	if k == 0 {
		return 1 - (float64(x*c)/(c-2.0) - x)
	}
	y := 1 - ((lo - float64(x*c)/(2.0-c)) - hi)
	if k >= -1021 {
		if k == 1024 {
			return y * 2.0 * two1023
		}
		return y * twopk
	}
	return y * twopk * twom1000
}

const (
	lg1   = 6.666666666666735130e-01
	lg2   = 3.999999999940941908e-01
	lg3   = 2.857142874366239149e-01
	lg4   = 2.222219843214978396e-01
	lg5   = 1.818357216161805012e-01
	lg6   = 1.531383769920937332e-01
	lg7   = 1.479819860511658591e-01
	two54 = 1.80143985094819840000e+16
)

func jsLog(x float64) float64 {
	const (
		ln2Hi = 6.93147180369123816490e-01
		ln2Lo = 1.90821492927058770002e-10
	)
	hx := highWord(x)
	k := int32(0)
	if hx < 0x00100000 {
		if uint32(hx&0x7fffffff)|lowWord(x) == 0 {
			return math.Inf(-1)
		}
		if hx < 0 {
			return math.NaN()
		}
		k -= 54
		x *= two54
		hx = highWord(x)
	}
	if hx >= 0x7ff00000 {
		return x + x
	}
	k += (hx >> 20) - 1023
	hx &= 0x000fffff
	i := (hx + 0x95f64) & 0x100000
	x = withHighWord(x, hx|(i^0x3ff00000))
	k += i >> 20
	f := x - 1.0
	if (0x000fffff & (2 + hx)) < 3 {
		if f == 0 {
			if k == 0 {
				return 0
			}
			dk := float64(k)
			return float64(dk*ln2Hi) + float64(dk*ln2Lo)
		}
		R := float64(f*f) * (0.5 - float64(0.33333333333333333*f))
		if k == 0 {
			return f - R
		}
		dk := float64(k)
		return float64(dk*ln2Hi) - ((R - float64(dk*ln2Lo)) - f)
	}
	s := f / (2.0 + f)
	dk := float64(k)
	z := s * s
	i = hx - 0x6147a
	w := z * z
	j := 0x6b851 - hx
	t1 := w * (lg2 + float64(w*(lg4+float64(w*lg6))))
	t2 := z * (lg1 + float64(w*(lg3+float64(w*(lg5+float64(w*lg7))))))
	i |= j
	R := t2 + t1
	if i > 0 {
		hfsq := 0.5 * f * f
		if k == 0 {
			return f - (hfsq - float64(s*(hfsq+R)))
		}
		return float64(dk*ln2Hi) - ((hfsq - (float64(s*(hfsq+R)) + float64(dk*ln2Lo))) - f)
	}
	if k == 0 {
		return f - float64(s*(f-R))
	}
	return float64(dk*ln2Hi) - ((float64(s*(f-R)) - float64(dk*ln2Lo)) - f)
}

func jsLog10(x float64) float64 {
	const (
		ivln10  = 4.34294481903251816668e-01
		log10Hi = 3.01029995663611771306e-01
		log10Lo = 3.69423907715893078616e-13
	)
	hx, lx := highWord(x), lowWord(x)
	k := int32(0)
	if hx < 0x00100000 {
		if uint32(hx&0x7fffffff)|lx == 0 {
			return math.Inf(-1)
		}
		if hx < 0 {
			return math.NaN()
		}
		k -= 54
		x *= two54
		hx = highWord(x)
		lx = lowWord(x)
	}
	if hx >= 0x7ff00000 {
		return x + x
	}
	if hx == 0x3ff00000 && lx == 0 {
		return 0
	}
	k += (hx >> 20) - 1023
	i := int32((uint32(k) & 0x80000000) >> 31)
	hx = (hx & 0x000fffff) | ((0x3ff - i) << 20)
	y := float64(k + i)
	x = fromWords(hx, lx)
	z := float64(y*log10Lo) + float64(ivln10*jsLog(x))
	return z + float64(y*log10Hi)
}

// kLog1p is FreeBSD's k_log1p: log(1 + f) - f + f*f/2 for f in [sqrt(2)/2 - 1, sqrt(2) - 1].
func kLog1p(f float64) float64 {
	s := f / (2.0 + f)
	z := s * s
	w := z * z
	t1 := w * (lg2 + float64(w*(lg4+float64(w*lg6))))
	t2 := z * (lg1 + float64(w*(lg3+float64(w*(lg5+float64(w*lg7))))))
	R := t2 + t1
	hfsq := 0.5 * f * f
	return s * (hfsq + R)
}

func jsLog2(x float64) float64 {
	const (
		ivln2hi = 1.44269504072144627571e+00
		ivln2lo = 1.67517131648865118353e-10
	)
	hx, lx := highWord(x), lowWord(x)
	k := int32(0)
	if hx < 0x00100000 {
		if uint32(hx&0x7fffffff)|lx == 0 {
			return math.Inf(-1)
		}
		if hx < 0 {
			return math.NaN()
		}
		k -= 54
		x *= two54
		hx = highWord(x)
	}
	if hx >= 0x7ff00000 {
		return x + x
	}
	if hx == 0x3ff00000 && lx == 0 {
		return 0.0
	}
	k += (hx >> 20) - 1023
	hx &= 0x000fffff
	i := (hx + 0x95f64) & 0x100000
	x = withHighWord(x, hx|(i^0x3ff00000))
	k += i >> 20
	y := float64(k)
	f := x - 1.0
	hfsq := 0.5 * f * f
	r := kLog1p(f)
	hi := withLowWord(f-hfsq, 0)
	lo := (f - hi) - hfsq + r
	valHi := hi * ivln2hi
	valLo := float64((lo+hi)*ivln2lo) + float64(lo*ivln2hi)
	w := y + valHi
	valLo += (y - w) + valHi
	valHi = w
	return valLo + valHi
}

func jsCbrt(x float64) float64 {
	const (
		B1 = 715094163
		B2 = 696219795
		P0 = 1.87595182427177009643
		P1 = -1.88497979543377169875
		P2 = 1.621429720105354466140
		P3 = -0.758397934778766047437
		P4 = 0.145996192886612446982
	)
	hx := uint32(highWord(x))
	low := lowWord(x)
	sign := hx & 0x80000000
	hx ^= sign
	if hx >= 0x7ff00000 {
		return x + x
	}
	var t float64
	if hx < 0x00100000 {
		if hx|low == 0 {
			return x
		}
		t = fromWords(0x43500000, 0)
		t *= x
		high := uint32(highWord(t))
		t = fromWords(int32(sign|((high&0x7fffffff)/3+B2)), 0)
	} else {
		t = fromWords(int32(sign|(hx/3+B1)), 0)
	}
	r := (t * t) * (t / x)
	t = t * ((P0 + float64(r*(P1+float64(r*P2)))) + float64(((r*r)*r)*(P3+float64(r*P4))))
	bits := (math.Float64bits(t) + 0x80000000) & 0xffffffffc0000000
	t = math.Float64frombits(bits)
	s := t * t
	r = x / s
	w := t + t
	r = (r - t) / (w + r)
	return t + float64(t*r)
}

// ---- hyperbolic ----

func jsCosh(x float64) float64 {
	const overflow = 710.4758600739439
	ix := highWord(x) & 0x7fffffff
	if ix < 0x3fd62e43 {
		t := jsExpm1(math.Abs(x))
		w := 1 + t
		if ix < 0x3c800000 {
			return w
		}
		return 1 + float64(t*t)/(w+w)
	}
	if ix < 0x40360000 {
		t := jsExp(math.Abs(x))
		return float64(0.5*t) + 0.5/t
	}
	if ix < 0x40862e42 {
		return 0.5 * jsExp(math.Abs(x))
	}
	if math.Abs(x) <= overflow {
		w := jsExp(0.5 * math.Abs(x))
		t := 0.5 * w
		return t * w
	}
	if ix >= 0x7ff00000 {
		return x * x
	}
	return math.Inf(1)
}

func jsSinh(x float64) float64 {
	const (
		overflow = 710.4758600739439
		twoM28   = 3.725290298461914e-9
		logMax   = 709.7822265625
		shuge    = 1.0e307
	)
	h := 0.5
	if x < 0 {
		h = -0.5
	}
	ax := math.Abs(x)
	if ax < 22 {
		if ax < twoM28 {
			return x
		}
		t := jsExpm1(ax)
		if ax < 1 {
			return h * (float64(2.0*t) - float64(t*t)/(t+1.0))
		}
		return h * (t + t/(t+1.0))
	}
	if ax < logMax {
		return h * jsExp(ax)
	}
	if ax <= overflow {
		w := jsExp(0.5 * ax)
		t := h * w
		return t * w
	}
	return x * shuge
}

func jsTanh(x float64) float64 {
	jx := highWord(x)
	ix := jx & 0x7fffffff
	if ix >= 0x7ff00000 {
		if jx >= 0 {
			return 1/x + 1
		}
		return 1/x - 1
	}
	var z float64
	if ix < 0x40360000 {
		if ix < 0x3e300000 {
			return x
		}
		if ix >= 0x3ff00000 {
			t := jsExpm1(2 * math.Abs(x))
			z = 1 - 2/(t+2)
		} else {
			t := jsExpm1(-2 * math.Abs(x))
			z = -t / (t + 2)
		}
	} else {
		z = 1 - 1.0e-300
	}
	if jx >= 0 {
		return z
	}
	return -z
}

// ---- pow ----

func ieeePow(x, y float64) float64 {
	huge, tiny := 1.0e300, 1.0e-300 // variables, so huge*huge overflows to Infinity as it runs
	const (
		two53  = 9007199254740992.0
		L1     = 5.99999999999994648725e-01
		L2     = 4.28571428578550184252e-01
		L3     = 3.33333329818377432918e-01
		L4     = 2.72728123808534006489e-01
		L5     = 2.30660745775561754067e-01
		L6     = 2.06975017800338417784e-01
		P1     = 1.66666666666666019037e-01
		P2     = -2.77777777770155933842e-03
		P3     = 6.61375632143793436117e-05
		P4     = -1.65339022054652515390e-06
		P5     = 4.13813679705723846039e-08
		lg2    = 6.93147180559945286227e-01
		lg2H   = 6.93147182464599609375e-01
		lg2L   = -1.90465429995776804525e-09
		ovt    = 8.0085662595372944372e-17
		cp     = 9.61796693925975554329e-01
		cpH    = 9.61796700954437255859e-01
		cpL    = -7.02846165095275826516e-09
		ivln2  = 1.44269504088896338700e+00
		ivln2H = 1.44269502162933349609e+00
		ivln2L = 1.92596299112661746887e-08
	)
	bp := [2]float64{1.0, 1.5}
	dpH := [2]float64{0.0, 5.84962487220764160156e-01}
	dpL := [2]float64{0.0, 1.35003920212974897128e-08}

	hx, lx := highWord(x), lowWord(x)
	hy, ly := highWord(y), lowWord(y)
	ix := hx & 0x7fffffff
	iy := hy & 0x7fffffff

	if uint32(iy)|ly == 0 {
		return 1
	}
	if ix > 0x7ff00000 || (ix == 0x7ff00000 && lx != 0) || iy > 0x7ff00000 || (iy == 0x7ff00000 && ly != 0) {
		return x + y
	}
	yisint := int32(0)
	if hx < 0 {
		if iy >= 0x43400000 {
			yisint = 2
		} else if iy >= 0x3ff00000 {
			k := (iy >> 20) - 0x3ff
			if k > 20 {
				j := ly >> uint(52-k)
				if j<<uint(52-k) == ly {
					yisint = 2 - int32(j&1)
				}
			} else if ly == 0 {
				j := iy >> uint(20-k)
				if j<<uint(20-k) == iy {
					yisint = 2 - (j & 1)
				}
			}
		}
	}
	if ly == 0 {
		if iy == 0x7ff00000 {
			if uint32(ix-0x3ff00000)|lx == 0 {
				return y - y
			} else if ix >= 0x3ff00000 {
				if hy >= 0 {
					return y
				}
				return 0
			}
			if hy < 0 {
				return -y
			}
			return 0
		}
		if iy == 0x3ff00000 {
			if hy < 0 {
				return 1 / x
			}
			return x
		}
		if hy == 0x40000000 {
			return x * x
		}
		if hy == 0x3fe00000 {
			if hx >= 0 {
				return math.Sqrt(x)
			}
		}
	}
	ax := math.Abs(x)
	if lx == 0 {
		if ix == 0x7ff00000 || ix == 0 || ix == 0x3ff00000 {
			z := ax
			if hy < 0 {
				z = 1 / z
			}
			if hx < 0 {
				if (ix-0x3ff00000)|yisint == 0 {
					z = math.NaN()
				} else if yisint == 1 {
					z = -z
				}
			}
			return z
		}
	}
	n := int32(uint32(hx)>>31) - 1
	if n|yisint == 0 {
		return math.NaN()
	}
	s := 1.0
	if n|(yisint-1) == 0 {
		s = -1.0
	}
	var t1, t2 float64
	if iy > 0x41e00000 {
		if iy > 0x43f00000 {
			if ix <= 0x3fefffff {
				if hy < 0 {
					return huge * huge
				}
				return tiny * tiny
			}
			if ix >= 0x3ff00000 {
				if hy > 0 {
					return huge * huge
				}
				return tiny * tiny
			}
		}
		if ix < 0x3fefffff {
			if hy < 0 {
				return s * huge * huge
			}
			return s * tiny * tiny
		}
		if ix > 0x3ff00000 {
			if hy > 0 {
				return s * huge * huge
			}
			return s * tiny * tiny
		}
		t := ax - 1
		w := float64(t*t) * (0.5 - float64(t*(0.3333333333333333333333-float64(t*0.25))))
		u := ivln2H * t
		v := float64(t*ivln2L) - float64(w*ivln2)
		t1 = withLowWord(u+v, 0)
		t2 = v - (t1 - u)
	} else {
		n = 0
		if ix < 0x00100000 {
			ax *= two53
			n -= 53
			ix = highWord(ax)
		}
		n += (ix >> 20) - 0x3ff
		j := ix & 0x000fffff
		ix = j | 0x3ff00000
		k := 0
		if j <= 0x3988E {
			k = 0
		} else if j < 0xBB67A {
			k = 1
		} else {
			k = 0
			n++
			ix -= 0x00100000
		}
		ax = withHighWord(ax, ix)
		u := ax - bp[k]
		v := 1 / (ax + bp[k])
		ss := u * v
		sH := withLowWord(ss, 0)
		tH := fromWords(((ix>>1)|0x20000000)+0x00080000+int32(k<<18), 0)
		tL := ax - (tH - bp[k])
		sL := v * ((u - float64(sH*tH)) - float64(sH*tL))
		s2 := ss * ss
		r := float64(s2*s2) * (L1 + float64(s2*(L2+float64(s2*(L3+float64(s2*(L4+float64(s2*(L5+float64(s2*L6))))))))))
		r += float64(sL * (sH + ss))
		s2 = sH * sH
		tH = withLowWord(3.0+s2+r, 0)
		tL = r - ((tH - 3.0) - s2)
		u = sH * tH
		v = float64(sL*tH) + float64(tL*ss)
		pH := withLowWord(u+v, 0)
		pL := v - (pH - u)
		zH := cpH * pH
		zL := float64(cpL*pH) + float64(pL*cp) + dpL[k]
		t := float64(n)
		t1 = withLowWord(((zH+zL)+dpH[k])+t, 0)
		t2 = zL - (((t1 - t) - dpH[k]) - zH)
	}
	y1 := withLowWord(y, 0)
	pL := float64((y-y1)*t1) + float64(y*t2)
	pH := y1 * t1
	z := pL + pH
	j := highWord(z)
	i := int32(lowWord(z))
	if j >= 0x40900000 {
		if (j-0x40900000)|i != 0 {
			return s * huge * huge
		}
		if pL+ovt > z-pH {
			return s * huge * huge
		}
	} else if j&0x7fffffff >= 0x4090cc00 {
		if uint32(j)-0xc090cc00|uint32(i) != 0 {
			return s * tiny * tiny
		}
		if pL <= z-pH {
			return s * tiny * tiny
		}
	}
	i = j & 0x7fffffff
	k := (i >> 20) - 0x3ff
	n = 0
	if i > 0x3fe00000 {
		n = j + (0x00100000 >> uint(k+1))
		k = ((n & 0x7fffffff) >> 20) - 0x3ff
		t := fromWords(n&^(0x000fffff>>uint(k)), 0)
		n = ((n & 0x000fffff) | 0x00100000) >> uint(20-k)
		if j < 0 {
			n = -n
		}
		pH -= t
	}
	t := withLowWord(pL+pH, 0)
	u := t * lg2H
	v := float64((pL-(t-pH))*lg2) + float64(t*lg2L)
	z = u + v
	w := v - (z - u)
	t = z * z
	t1 = z - float64(t*(P1+float64(t*(P2+float64(t*(P3+float64(t*(P4+float64(t*P5)))))))))
	r := float64(z*t1) / ((t1 - 2) - (w + float64(z*w)))
	z = 1 - (r - z)
	j = highWord(z)
	j += int32(uint32(n) << 20)
	if j>>20 <= 0 {
		z = math.Ldexp(z, int(n))
	} else {
		z = withHighWord(z, highWord(z)+int32(uint32(n)<<20))
	}
	return s * z
}
