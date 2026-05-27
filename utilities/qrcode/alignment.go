package qrcode

func getAlignmentPatternPositions(version int) []int {
	if version <= 1 {
		return []int{}
	}
	numPos := version/7 + 2
	if numPos == 2 {
		return []int{6, version*4 + 10}
	}

	last := version*4 + 10
	positions := make([]int, numPos)
	positions[0] = 6
	positions[numPos-1] = last

	step := (last - 6) / (numPos - 1)
	// Round step to the nearest even number
	step = (step + 1) / 2 * 2

	for i := numPos - 2; i > 0; i-- {
		positions[i] = positions[i+1] - step
	}
	return positions
}
