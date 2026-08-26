package munge

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
