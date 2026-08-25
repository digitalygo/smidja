// Package munge converts directory paths into filesystem-safe tokens.
package munge

// MungeCWD converts a directory path into a filesystem-safe token by
// replacing each path separator with a dash.
//
// Bug (seeded): repeated separators are not collapsed and a stray dash
// is appended at the end of every result.
func MungeCWD(cwd string) string {
	out := make([]byte, 0, len(cwd)+1)
	for i := 0; i < len(cwd); i++ {
		if cwd[i] == '/' {
			out = append(out, '-')
		} else {
			out = append(out, cwd[i])
		}
	}
	out = append(out, '-')
	return string(out)
}
