package qrcode

type Module uint32

const (
	FlagActive Module = 1 << 0

	FlagFinder    Module = 1 << 1
	FlagCenter    Module = 1 << 2
	FlagAlignment Module = 1 << 3

	FlagBorder Module = 1 << 4
)

func (m Module) Active() bool      { return m&FlagActive != 0 }
func (m Module) IsCenter() bool    { return m&FlagCenter != 0 }
func (m Module) IsAlignment() bool { return m&FlagAlignment != 0 }
func (m Module) IsFinder() bool    { return m&FlagFinder != 0 }
func (m Module) IsBorder() bool    { return m&FlagBorder != 0 }
