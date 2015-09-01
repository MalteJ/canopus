package canopus

type QueueItem struct {
	/*
	Request
	Priority
	TransmitRetries
	AddedTS

	 */
}

type Queue interface {
	Start()
	Stop()
	Push(*QueueItem)
	Pop() *QueueItem
	Clear()
	Get(string) *QueueItem
	Send() error
}

func NewDefaultQueue() Queue {
	return &DefaultQueue{}
}

type DefaultQueue struct {

}

func (q *DefaultQueue) Start() {

}

func (q *DefaultQueue) Stop() {

}

func (q *DefaultQueue) Push(*QueueItem) {

}

func (q *DefaultQueue) Pop() *QueueItem {

}

func (q *DefaultQueue) Clear() {

}

func (q *DefaultQueue) Get(string) *QueueItem {

}

func (q *DefaultQueue) Send() error {

}


/*
	Operations
		Push
		Pop
		Get
		Clear
		Send

	QueueItem

	Periodically:
		Try send items in queue via go routine
		if fail,
			if max_retransmit exceeded
				fire OnTimeout event
			else
				increment max_retransmit


 */