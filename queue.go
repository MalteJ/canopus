package canopus
import (
	"time"
	"container/heap"
)

type Item struct {
	value    	*CoapRequest 	// The value of the item; arbitrary.
	priority 	int    			// The priority of the item in the queue.
								// The index is needed by update and is maintained by the heap.Interface methods.
	index 		int 			// The index of the item in the heap.
	retries 	int
	ts 			*time.Time
}

type PriorityQueue []*Item

func (pq PriorityQueue) Len() int { return len(pq) }

func (pq PriorityQueue) Less(i, j int) bool {
	// We want Pop to give us the highest, not lowest, priority so we use greater than here.
	return pq[i].priority > pq[j].priority
}

func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *PriorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*Item)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *PriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	item.index = -1 // for safety
	*pq = old[0 : n-1]
	return item
}

// update modifies the priority and value of an Item in the queue.
func (pq *PriorityQueue) update(item *Item, value string, priority int) {
	item.value = value
	item.priority = priority
	heap.Fix(pq, item.index)
}

type Queue interface {
	Start()
	Stop()
	Push(*Item)
	Pop()
	Clear()
	Get(string) *Item
	Send() error
}

func NewDefaultQueue() Queue {
	return &DefaultQueue{}
}

type DefaultQueue struct {
	priorityQueue 	*PriorityQueue
}

func (q *DefaultQueue) Start() {
	// start gofunc for sending
	
}

func (q *DefaultQueue) Stop() {

}

func (q *DefaultQueue) Push(i *Item) {
	q.priorityQueue.Push(i)
}

func (q *DefaultQueue) Pop() *Item {
	return q.priorityQueue.Pop()
}

func (q *DefaultQueue) Clear() {

}

func (q *DefaultQueue) Get(id string) *Item {
	return q.Get(id)
}

func (q *DefaultQueue) Send() error {
	return nil
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