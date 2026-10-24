package ui

// CompileError prepares the error page seen from routeDir, as CompileNotFound prepares the not-found
// page (20/10/2026): the nearest error.ui walking up takes the place of content.ui inside the same page
// frame. It returns nil when there is none. The page reads one name, message: what the handler gave
// error(…), never an error of the engine.
func (s *Site) CompileError(routeDir string) (*Page, error) {
	file := s.find(routeDir, "error")
	if file == "" {
		return nil, nil
	}
	return s.compile(routeDir, file, pageError)
}
