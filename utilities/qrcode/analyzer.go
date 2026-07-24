package qrcode

// Analyze converts the raw boolean matrix of the QR code into a 2D grid of Module flags.
// It maps the cells to semantic components (Finders, Center Logo area, Alignments, and Data cells)
// and computes connectivity flags for active data cells.

func Analyze(matrix [][]bool, options *Options) [][]Module {
	size := len(matrix)
	grid := make([][]Module, size)
	for i := range grid {
		grid[i] = make([]Module, size)
	}

	version := (size - 17) / 4

	// 1. Calculate center logo bounds
	centerCells := int(options.Logo.Size)

	// 2. Calculate alignment centers
	var alignmentCenters []int
	if version > 1 {
		alignmentCenters = getAlignmentPatternPositions(version)
	}

	// 3. First pass: Set active status and identify functional regions (finders, center, alignment)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var m Module

			if matrix[y][x] {
				m |= FlagActive
			}

			// Check Finders
			if (x >= 0 && x < 7 && y >= 0 && y < 7) ||
				(x >= size-7 && x < size && y >= 0 && y < 7) ||
				(x >= 0 && x < 7 && y >= size-7 && y < size) {
				m |= FlagFinder
			} else if centerCells > 0 && isWithinCenter(x, y, size, centerCells) {
				m |= FlagCenter
			} else if len(alignmentCenters) > 0 && isWithinAlignment(x, y, size, alignmentCenters) {
				m |= FlagAlignment
			}

			grid[y][x] = m
		}
	}

	// 4. Second pass: Calculate neighbors (only for active data cells, not functional patterns)
	return grid
}

func isWithinCenter(x, y, size, centerCells int) bool {
	centerStart := (size - centerCells) / 2
	centerEnd := centerStart + centerCells
	return x >= centerStart && x < centerEnd && y >= centerStart && y < centerEnd
}

func isWithinAlignment(x, y, size int, centers []int) bool {
	for _, cx := range centers {
		for _, cy := range centers {
			if (cx == 6 && cy == 6) || (cx == 6 && cy == size-7) || (cx == size-7 && cy == 6) {
				continue
			}
			if x >= cx-2 && x <= cx+2 && y >= cy-2 && y <= cy+2 {
				return true
			}
		}
	}
	return false
}
