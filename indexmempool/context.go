package indexmempool

type Context[T any] struct {
	index     int
	action    ActionType
	handlers  []HandlerFunc[T]
	slotIndex int
	fn        func(*T)
	err       error
}

func (c *Context[T]) Next() {
	c.index++
	for c.index < len(c.handlers) {
		if c.handlers[c.index] != nil {
			c.handlers[c.index](c)
		}
		c.index++
	}
}

func (c *Context[T]) Index() int         { return c.slotIndex }
func (c *Context[T]) Action() ActionType { return c.action }
func (c *Context[T]) Err() error         { return c.err }

func (c *Context[T]) reset() {
	*c = Context[T]{}
}
