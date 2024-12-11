package jenkins

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBuildURLEqual(t *testing.T) {
	type testcase struct {
		URL1          string
		URL2          string
		ShouldBeEqual bool
	}
	tcs := []testcase{
		{
			URL1:          "https://localhost/job/node-integrationtests/job/abc%252FCORE-13239/8",
			URL2:          "https://localhost/job/node-integrationtests/job/abc%252FCORE-13239/8/display/redirect?page=tests",
			ShouldBeEqual: true,
		},

		{
			URL1:          "https://localhost/job/ci-ng/job/PR-26914/21/",
			URL2:          "https://localhost/job/ci-ng/job/PR-26914/21/",
			ShouldBeEqual: true,
		},

		{
			URL1:          "https://localhost/job/ci-ng/21/",
			URL2:          "https://localhost/job/ci-ng/21/",
			ShouldBeEqual: true,
		},
	}

	for i, tc := range tcs {
		// /job/(\S)*/\d+
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Logf("comparing %q vs %q", tc.URL1, tc.URL2)

			b1, err := ParseBuildURL(tc.URL1)
			require.NoError(t, err)

			b2, err := ParseBuildURL(tc.URL2)
			require.NoError(t, err)

			if tc.ShouldBeEqual {
				assert.Equal(t, b1.JobName, b2.JobName)
				assert.Equal(t, b1.Number, b2.Number)
			}

			assert.Equal(t, tc.ShouldBeEqual, b1.String() == b2.String())
		})
	}
}

func TestParseURLFailsOnEmptyURL(t *testing.T) {
	b, err := ParseBuildURL("")
	require.Error(t, err)
	assert.Nil(t, b)
}

func TestPareURLFailsOnMissingURLPath(t *testing.T) {
	b, err := ParseBuildURL("https://localhost")
	require.Error(t, err)
	assert.Nil(t, b)
}

func TestNormalizeBuildURLFailsOnMisformedURLPath(t *testing.T) {
	b, err := ParseBuildURL("https://localhost/3")
	require.Error(t, err)
	assert.Nil(t, b)
}

func TestParseMultibranchBuildURL(t *testing.T) {
	b, err := ParseBuildURL("https://jenkins.invalid/job/ci-ng/job/master/8866/")
	require.NoError(t, err)
	assert.Equal(t, "ci-ng/job/master", b.JobName)
	assert.Equal(t, int64(8866), b.Number)
}

func TestParseBuildURL(t *testing.T) {
	b, err := ParseBuildURL("https://jenkins.invalid/job/build/3")
	require.NoError(t, err)
	assert.Equal(t, "build", b.JobName)
	assert.Equal(t, int64(3), b.Number)
}
