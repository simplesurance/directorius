package jenkins

type Job struct {
	relURL     string
	parameters map[string]string
}

func (j *Job) String() string {
	return j.relURL
}
