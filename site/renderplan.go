package site

// RenderPlan is the immutable template/render snapshot owned by one
// Generation. Its concrete implementation remains in package work while it
// still maps route nodes to the render package.
type RenderPlan interface {
	Close()
}
