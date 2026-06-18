package queue

type Context[T any] struct {
	index    int
	Action   ActionType
	handlers []HandlerFunc[T]
	data     T
	err      error
}

func (c *Context[T]) Next() {
	c.index++
	for c.index < int(len(c.handlers)) {
		if c.handlers[c.index] != nil {
			c.handlers[c.index](c)
		}
		c.index++
	}
}

func (c *Context[T]) reset() {
	*c = Context[T]{}
}
