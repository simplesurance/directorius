package jenkins

type basicAuth struct {
	user     string
	password string
}

type Server struct {
	url  string
	auth *basicAuth
}

func NewServer(url, user, password string) *Server {
	return &Server{
		url:  url,
		auth: &basicAuth{user: user, password: password},
	}
}
