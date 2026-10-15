package highlight

// tokenizeText is text: the source as one plain token, for code that is not in
// any language and for showing a listing exactly as written.
func tokenizeText(scan *scanner) { scan.emitRest(Plain) }
