package pagestypes

import (
	"hash/fnv"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Person is a GitHub user shown with an initials avatar.
type Person struct {
	Login   string
	URL     string
	Initial string
	// Hue is a stable HSL hue (0-359) derived from Login, used to color the avatar.
	Hue uint32
}

func NewPerson(login string) *Person {
	if login == "" {
		return nil
	}

	profileURL, err := url.JoinPath("https://github.com", login)
	if err != nil {
		profileURL = ""
	}

	h := fnv.New32a()
	_, _ = h.Write([]byte(login))

	return &Person{
		Login:   login,
		URL:     profileURL,
		Initial: initial(login),
		Hue:     h.Sum32() % 360,
	}
}

func NewPersons(logins []string) []*Person {
	result := make([]*Person, 0, len(logins))
	for _, l := range logins {
		if p := NewPerson(l); p != nil {
			result = append(result, p)
		}
	}
	return result
}

func initial(login string) string {
	for _, r := range login {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return strings.ToUpper(string(r))
		}
	}

	r, _ := utf8.DecodeRuneInString(login)
	return string(r)
}
