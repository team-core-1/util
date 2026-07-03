package indexpool

type Context[T any] struct {
	index    int
	action   ActionType
	handlers []HandlerFunc[T]
	idx      int
	fn       func(*T)
	err      error
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

func (c *Context[T]) Idx() int           { return c.idx }
func (c *Context[T]) Action() ActionType { return c.action }
func (c *Context[T]) Err() error         { return c.err }

func (c *Context[T]) reset() {
	*c = Context[T]{}
}
