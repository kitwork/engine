# KitID — Sortable IDs for modern systems

KitID is not just a "UUID replacement." It is an industrial-grade, strictly sortable, and highly aesthetic unique identifier generator. By completely abandoning the traditional `bytes -> baseN` encoding in favor of a `timestamp -> permutation path -> shuffled identity` architecture, KitID brings "personality" and an organic feel to database infrastructure.

---

## 🌟 The KitID Identity

### 1. Organic Aesthetics
Unlike mechanical and highly repetitive IDs (like UUID `550e8400-e29b-41d4-a716-446655440000` or ULID `01HX7ZK8M8W6M6ZP0M7TQ7J2YF`), KitIDs look incredibly diverse and naturally random (e.g., `027ctnihvpzsmr8xjd5luwfqgkay36o941be`).

### 2. No Repeating Characters (Visual Uniqueness)
A highly unique feature in the ID space: **No character in a KitID ever repeats.** You will never see patterns like `aaa` or `111`. This dramatically increases human readability, prevents clustering patterns, and establishes a strong visual signature.

### 3. Strict Lexicographical Sortability
It is mathematically guaranteed that `id1 < id2` (ASCII alphabetical sort) exactly mirrors `time1 < time2`. Despite using a shrinking charset permutation, because the original character pool is strictly sorted (`0-9a-z`), Horner's method guarantees that chronologically newer IDs will always extract lexicographically larger characters. **It is 100% strict.**

---

## ⚔️ The Ultimate Comparisons

KitID operates as a hybrid of the best systems in the industry: taking Sortability from **ULID**, Epoch strategies from **KSUID**, Shuffled aesthetics from **NanoID**, Sequences from **Snowflake**, and a Time-first philosophy from **UUIDv7**.

### KitID vs UUIDv7 (The New Standard)
| Feature | UUIDv7 | KitID |
| :--- | :--- | :--- |
| **Time Sortable** | ✅ Strict | ✅ Strict |
| **Decode Time** | ✅ | ✅ |
| **Standard RFC** | ✅ | ❌ (Custom) |
| **Human Readable**| Medium | **High** |
| **Visual Uniqueness**| Low (Repetitive) | **High (Organic)** |
| **Custom Charset**| ❌ | ✅ |

*UUIDv7 is better for massive enterprise standards. KitID is better for branding, aesthetics, and compactness.*

### KitID vs ULID (The Closest Rival)
| Feature | ULID | KitID |
| :--- | :--- | :--- |
| **Sortable** | ✅ | ✅ |
| **Decode Time** | ✅ | ✅ |
| **Monotonic Mode**| ✅ | ✅ |
| **No-Repeat Chars**| ❌ | ✅ |
| **Organic Look** | ❌ | ✅ |
| **Collision Handling**| Strong | **Extreme (12-bit Seq)** |

*ULID is very clean and standard-ish. KitID looks more natural, is harder to predict, and has much higher visual uniqueness.*

### KitID vs Nano ID
| Feature | NanoID | KitID |
| :--- | :--- | :--- |
| **Sortable** | ❌ | ✅ |
| **Decode Time** | ❌ | ✅ |
| **Random Aesthetic**| High | **Very High** |
| **Entropy** | Very High | High ($23!$) |

*KitID is effectively a NanoID + ULID hybrid.*

---

## ⚙️ Architecture & Mechanics

### 1. The 13-Character Time Prefix (Permutation Path)
Instead of a plain base encoding, time is encoded as a permutation path over a shrinking charset (Base 24 to Base 36) using Horner's method. This creates a fixed-width 13-character prefix that is strictly monotonically increasing.

### 2. The 12-Bit Atomic Sequence (Snowflake Gene)
KitID integrates a 12-bit atomic counter (`sync/atomic` in Go, `SEQUENCE` in Postgres). If multiple IDs are generated within the exact same time window, KitID safely masks the lowest 12 bits of the nanosecond timestamp and overrides them with the sequence. 
This guarantees **4,096 collision-free, strictly ordered IDs per exact microsecond** before relying on randomness.

### 3. The 23-Character Random Tail (Partial Fisher-Yates)
The remaining 23 characters are filled using a lock-free Partial Fisher-Yates shuffle on the *unused* characters. This injects **~74 bits of entropy** ($23!$ possibilities) as an impenetrable secondary defense.

### 4. Zero-Allocation Engine
Built with `math/rand/v2` and strict stack arrays (`[256]byte`). KitID operates without allocating any heap memory (Zero-GC), achieving insane performance limits.

---

## 🚀 Usage (Golang)

```go
package main

import (
	"fmt"
	"github.com/kitwork/kitid"
)

func main() {
	// Generate a standard KitID (36 characters)
	myId := id.Entity()
	fmt.Println("Generated KitID:", myId)
	// Output: 027ctnihvpzsmr8xjd5luwfqgkay36o941be

	// Decode KitID back to a Timestamp
	timestamp, err := id.DecodeEntity(myId)
	if err == nil {
		fmt.Println("Created At:", timestamp.UTC())
	}
}
```

## 🐘 Usage (PostgreSQL Native)

Seamless Backend-to-Database parity. No more drifting ID formats.

```sql
-- Create the atomic sequence
CREATE SEQUENCE public.kitworkid_seq MAXVALUE 4095 CYCLE;

-- Generate natively in SQL
SELECT public.kitworkid();
-- Output: 027ctnihvpzsmr8xjd5luwfqgkay36o941be

-- Decode KitID strictly in SQL
SELECT public.kitworkid_decode('027ctnihvpzsmr8xjd5luwfqgkay36o941be');
-- Output: 2026-05-18 03:10:28.39737+00
```

---

## 🛑 Current Limitations & Future Roadmap

1. **Algorithm Versioning:** Currently, decoding assumes the default `charset36` and a specific permutation strategy. Future versions should consider an algorithmic version prefix (e.g., `A...`, `B...`) to prevent breaking decoding if the algorithm evolves.
2. **Standardization:** KitID lacks a formal RFC or binary `BYTEA` storage format, which systems like KSUID or UUID natively boast.

*KitID brings personality to identifiers. It is the perfect choice when you want your application's data keys to look as beautiful and thoughtfully designed as the application itself.*
