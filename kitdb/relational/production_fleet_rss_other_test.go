//go:build !windows

package relational

func shoppingFleetCurrentRSS() (uint64, bool) {
	return 0, false
}
