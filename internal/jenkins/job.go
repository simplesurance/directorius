package jenkins

type Job struct {
	relURL         string
	parametersJSON []byte
}

func (j *Job) String() string {
	return j.relURL
}
