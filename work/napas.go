package work

import (
	"strconv"

	"github.com/kitwork/engine/helpers/napas"
	"github.com/kitwork/engine/value"
)

type Napas struct {
	core *napas.Napas
}

func (w *KitWork) Napas() *Napas {
	return &Napas{
		core: napas.New(),
	}
}

func (n *Napas) Bank(binVal value.Value, accountVal value.Value) *Napas {
	n.core.Bank(binVal.Text(), accountVal.Text())
	return n
}

func (n *Napas) Amount(v value.Value) *Napas {
	switch v.K {
	case value.String:
		n.core.Amount(v.Text())
	case value.Number:
		val := v.N
		var amt string
		if val == float64(int64(val)) {
			amt = strconv.FormatInt(int64(val), 10)
		} else {
			amt = strconv.FormatFloat(val, 'f', -1, 64)
		}
		n.core.Amount(amt)
	}
	return n
}

func (n *Napas) Info(v value.Value) *Napas {
	n.core.Info(v.Text())
	return n
}

func (n *Napas) Service(v value.Value) *Napas {
	n.core.Service(v.Text())
	return n
}

func (n *Napas) ToAccount() *Napas {
	n.core.ToAccount()
	return n
}

func (n *Napas) ToCard() *Napas {
	n.core.ToCard()
	return n
}

func (n *Napas) Receiver(v value.Value) *Napas {
	n.core.Receiver(v.Text())
	return n
}

func (n *Napas) ReceiverName(v value.Value) *Napas {
	return n.Receiver(v)
}

func (n *Napas) City(v value.Value) *Napas {
	n.core.City(v.Text())
	return n
}

func (n *Napas) Dynamic() *Napas {
	n.core.Dynamic()
	return n
}

func (n *Napas) Static() *Napas {
	n.core.Static()
	return n
}

func (n *Napas) Country(v value.Value) *Napas {
	n.core.Country(v.Text())
	return n
}

func (n *Napas) Payload() string {
	return n.core.Payload()
}

func (n *Napas) Validate() error {
	return n.core.Validate()
}

func (n *Napas) Generate() (value.Value, error) {
	if err := n.core.Validate(); err != nil {
		return value.Value{}, err
	}
	return value.New(n.core.Payload()), nil
}
