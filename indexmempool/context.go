package indexmempool

type Context[T any] struct {
	index     int
	action    ActionType
	handlers  []HandlerFunc[T]
	slotIndex int
	fn        func(*T)
	err       error
}

// Next는 체인의 다음 핸들러부터 종단 핸들러(실제 Get/Put/Access/AccessLock 연산)까지 순차적으로 실행합니다.
// 미들웨어에서 Next를 호출하면 후속 핸들러를 감싸는 형태가 되어 전/후처리를 수행할 수 있습니다.
//
// [설계 방침: 체인 중단(취소) 미지원]
//   - 미들웨어가 연산을 중단하거나 취소하는 기능은 의도적으로 지원하지 않습니다.
//   - 따라서 미들웨어에서 Next를 호출하지 않더라도 후속 핸들러와 종단 핸들러는 그대로 실행됩니다.
//     (Next 호출 여부는 "감싸는 위치"만 결정할 뿐, 실행 여부를 결정하지 않습니다.)
//   - 미들웨어는 로깅, 실행 시간 측정, 통계 수집 등 부수 작업 용도로 사용하십시오.
//     연산 자체를 거부해야 한다면 미들웨어가 아니라 호출 측에서 사전 검사하십시오.
//
// [패닉 처리]
// 모든 단계(Get/Put/Access/AccessLock)의 미들웨어는 해당 메서드를 호출한 고루틴에서 실행됩니다.
// 미들웨어에서 발생한 panic은 호출 측으로 전파되므로 호출 측에서 recover할 수 있습니다.
// 라이브러리 내부에는 recover 지점이 없습니다.
// (단, 슬롯의 사용 중 표시와 슬롯 락은 panic 시에도 정상 복구됩니다.)
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
