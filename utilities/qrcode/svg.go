package qrcode

import (
	"fmt"

	"github.com/beevik/etree"
)

type Svg struct {
	document *etree.Document
	element  *Element
}

func NewSVG(size int) *Svg {
	document := etree.NewDocument()
	element := document.CreateElement("svg")

	element.CreateAttr("viewBox", fmt.Sprintf("0 0 %d %d", size, size))
	element.CreateAttr("version", "1.1")
	element.CreateAttr("xmlns", "http://www.w3.org/2000/svg")
	element.CreateAttr("xmlns:xlink", "http://www.w3.org/1999/xlink")
	element.CreateAttr("name", "main")
	element.CreateAttr("fill", "none")
	element.CreateAttr("style", "width: 100%;height: 100%;")

	return &Svg{
		document: document,
		element:  &Element{element: element},
	}
}

func (svg *Svg) NewElement(name string) *Element {
	return NewElement(svg.element.element, name)
}

func (svg *Svg) WriteToString() (string, error) {
	return svg.document.WriteToString()
}

type Element struct {
	element *etree.Element
}

func NewElement(e *etree.Element, name string) *Element {
	return &Element{element: e.CreateElement(name)}
}

func (e *Element) Append(element *etree.Element) *Element {
	e.element.AddChild(element)
	return e
}

func (e *Element) New(name string) *Element {
	return &Element{element: e.element.CreateElement(name)}
}

func (e *Element) Attribute(name string, value string) *Element {
	e.element.CreateAttr(name, value)
	return e
}

func (e *Element) Name(name string) *Element {
	e.element.CreateAttr("name", name)
	return e
}

func (e *Element) Style(style string) *Element {
	e.element.CreateAttr("style", style)
	return e
}

func (e *Element) X(x float64) *Element {
	e.element.CreateAttr("x", fmt.Sprintf("%.2f", x))
	return e
}

func (e *Element) Y(y float64) *Element {
	e.element.CreateAttr("y", fmt.Sprintf("%.2f", y))
	return e
}

func (e *Element) XY(x float64, y float64) *Element {
	return e.X(x).Y(y)
}

func (e *Element) Width(width float64) *Element {
	e.element.CreateAttr("width", fmt.Sprintf("%.2f", width))
	return e
}

func (e *Element) Height(height float64) *Element {
	e.element.CreateAttr("height", fmt.Sprintf("%.2f", height))
	return e
}

func (e *Element) RX(rx float64) *Element {
	e.element.CreateAttr("rx", fmt.Sprintf("%.2f", rx))
	return e
}

func (e *Element) RY(ry float64) *Element {
	e.element.CreateAttr("ry", fmt.Sprintf("%.2f", ry))
	return e
}

func (e *Element) Rounded(r float64) *Element {
	return e.RX(r).RY(r)
}

func (e *Element) RoundedX(r float64) *Element {
	return e.RX(r)
}

func (e *Element) RoundedY(r float64) *Element {
	return e.RY(r)
}

func (e *Element) Opacity(opacity float64) *Element {
	e.element.CreateAttr("opacity", fmt.Sprintf("%.2f", opacity))
	return e
}

func (e *Element) Fill(fill string) *Element {
	return e.Attribute("fill", fill)
}

func (e *Element) Stroke(stroke string) *Element {
	return e.Attribute("stroke", stroke)
}

func (e *Element) StrokeWidth(strokeWidth float64) *Element {
	return e.Attribute("stroke-width", fmt.Sprintf("%.2f", strokeWidth))
}

func (e *Element) Data(name string, value string) *Element {
	e.element.CreateAttr("data-"+name, value)
	return e
}

func (e *Element) Dataset(data map[string]string) *Element {
	for name, value := range data {
		e.Data(name, value)
	}
	return e
}

func (e *Element) DashArray(dashArray float64) *Element {
	return e.Attribute("stroke-dasharray", fmt.Sprintf("%.2f", dashArray))
}

func (e *Element) DashOffset(dashOffset float64) *Element {
	return e.Attribute("stroke-dashoffset", fmt.Sprintf("%.2f", dashOffset))
}
