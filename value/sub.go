package value

type Sub uint8

const (
	None     Sub = 0
	SafeHTML Sub = 1 // Marks content as Safe HTML (no escape needed)
)
