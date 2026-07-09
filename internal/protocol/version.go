package protocol

const Version = "v1"

func Path(route string) string {
	if route == "" || route[0] != '/' {
		return "/" + Version + "/" + route
	}
	return "/" + Version + route
}
