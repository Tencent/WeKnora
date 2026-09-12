package types

// QAPair represents a complete QA example with question, related passages and answer
type QAPair struct {
	QID                      int         // Internal numeric question ID
	DatasetQID               string      // Stable source dataset question ID
	Question                 string      // Question text
	PIDs                     []int       // Related passage IDs
	PIDGrades                map[int]int // Graded relevance keyed by passage ID
	RetrievalLabelsAvailable bool        // Whether this question has an explicit relevance label set
	Passages                 []string    // Related passage texts
	Corpus                   []string    // Complete immutable corpus in PID-index order
	AID                      int         // Answer ID
	Answer                   string      // Answer text
}
