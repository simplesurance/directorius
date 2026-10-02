package pagestypes

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewPerson(t *testing.T) {
	p := NewPerson("octo-cat")
	require.Equal(t, "octo-cat", p.Login)
	require.Equal(t, "https://github.com/octo-cat", p.URL)
	require.Equal(t, "O", p.Initial)
	require.Less(t, p.Hue, uint32(360))
	require.Equal(t, p.Hue, NewPerson("octo-cat").Hue, "hue must be stable")
}

func TestNewPerson_emptyLogin(t *testing.T) {
	require.Nil(t, NewPerson(""))
}

func TestNewPersons_skipsEmptyLogins(t *testing.T) {
	persons := NewPersons([]string{"a", "", "b"})
	require.Len(t, persons, 2)
	require.Equal(t, "a", persons[0].Login)
	require.Equal(t, "b", persons[1].Login)
}

func TestInitial_skipsLeadingSymbols(t *testing.T) {
	require.Equal(t, "B", initial("_bot"))
}
