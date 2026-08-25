package main

type Choice struct {
	Type        string
	Node        int
	From        int
	To          int
	Op          string
	Step        int
	MaxMessages int
}

func (c Choice) Copy() Choice {
	return Choice{
		Type:        c.Type,
		Node:        c.Node,
		From:        c.From,
		To:          c.To,
		Op:          c.Op,
		Step:        c.Step,
		MaxMessages: c.MaxMessages,
	}
}

type Trace struct {
	Choices []Choice
}

func (t *Trace) Copy() *Trace {
	new := &Trace{
		Choices: make([]Choice, len(t.Choices)),
	}
	for i, ch := range t.Choices {
		new.Choices[i] = ch.Copy()
	}
	return new
}

func NewTrace() *Trace {
	return &Trace{
		Choices: make([]Choice, 0),
	}
}

func (t *Trace) Add(ch Choice) {
	t.Choices = append(t.Choices, ch.Copy())
}

type Event struct {
	Name   string
	Node   int `json:"-"`
	Params map[string]interface{}
	Reset  bool
}

func (e Event) Copy() Event {
	new := Event{
		Name:   e.Name,
		Node:   e.Node,
		Params: make(map[string]interface{}),
		Reset:  e.Reset,
	}
	for k, v := range e.Params {
		new.Params[k] = v
	}
	return new
}

type EventTrace struct {
	Events []Event
}

func NewEventTrace() *EventTrace {
	return &EventTrace{
		Events: make([]Event, 0),
	}
}

func (e *EventTrace) Copy() *EventTrace {
	new := &EventTrace{
		Events: make([]Event, len(e.Events)),
	}
	for i, e := range e.Events {
		new.Events[i] = e.Copy()
	}
	return new
}

func (et *EventTrace) Add(e Event) {
	et.Events = append(et.Events, e.Copy())
}
