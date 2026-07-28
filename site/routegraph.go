package site

// RouteGraph is the executable route snapshot owned by one Generation.
//
// The concrete graph currently lives in package work because its nodes contain
// compiled Kitwork programs and handler lambdas. Keeping only the lifecycle
// contract here avoids a site <-> work import cycle while making ownership
// explicit.
type RouteGraph interface {
	Close()
}
