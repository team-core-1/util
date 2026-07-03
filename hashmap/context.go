package hashmap

type Context[K comparable, V any] struct {
	index    int
	action   ActionType
	handlers []HandlerFunc[K, V]

	key   K
	value V
	err   error
}

func (c *Context[K, V]) Next() {
	c.index++
	for c.index < len(c.handlers) {
		if c.handlers[c.index] != nil {
			c.handlers[c.index](c)
		}
		c.index++
	}
}

func (c *Context[K, V]) Action() ActionType { return c.action }
func (c *Context[K, V]) Key() K             { return c.key }
func (c *Context[K, V]) Value() V           { return c.value }
func (c *Context[K, V]) Err() error         { return c.err }

func (c *Context[K, V]) reset() { *c = Context[K, V]{} }
