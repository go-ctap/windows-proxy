package transport

type Config struct {
	Address                 string
	Debug                   bool
	AllowAuthenticatedUsers bool
	AllowedClientSIDs       []string
}
