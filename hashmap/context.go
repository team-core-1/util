package hashmap

type Context[K comparable, V any] struct {
	index    int
	action   ActionType
	handlers []HandlerFunc[K, V]

	key   K
	value V
	err   error
}

// Next는 체인의 다음 핸들러부터 종단 핸들러(실제 Put/Get/Delete 연산)까지 순차적으로 실행합니다.
// 미들웨어에서 Next를 호출하면 후속 핸들러를 감싸는 형태가 되어 전/후처리를 수행할 수 있습니다.
//
// [설계 방침: 체인 중단(취소) 미지원]
//   - 미들웨어가 연산을 중단하거나 취소하는 기능은 의도적으로 지원하지 않습니다.
//   - 따라서 미들웨어에서 Next를 호출하지 않더라도 후속 핸들러와 종단 핸들러는 그대로 실행됩니다.
//     (Next 호출 여부는 "감싸는 위치"만 결정할 뿐, 실행 여부를 결정하지 않습니다.)
//   - 미들웨어는 로깅, 실행 시간 측정, 통계 수집 등 부수 작업 용도로 사용하십시오.
//     연산 자체를 거부해야 한다면 미들웨어가 아니라 호출 측에서 사전 검사하십시오.
//
// [사용 예시]
//
//	// 실행 시간 측정 미들웨어
//	hm.Use(func(c *hashmap.Context[string, int]) {
//		start := time.Now()
//		c.Next() // 실제 연산 수행
//		log.Printf("%v took %v (err=%v)", c.Action(), time.Since(start), c.Err())
//	})
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
