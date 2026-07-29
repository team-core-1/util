package pipequeue

type Context[T any] struct {
	index    int
	action   ActionType
	handlers []HandlerFunc[T]
	data     T
	err      error
}

// Next는 체인의 다음 핸들러부터 종단 핸들러(실제 Put/Pipe 연산)까지 순차적으로 실행합니다.
// 미들웨어에서 Next를 호출하면 후속 핸들러를 감싸는 형태가 되어 전/후처리를 수행할 수 있습니다.
//
// [설계 방침: 체인 중단(취소) 미지원]
//   - 미들웨어가 연산을 중단하거나 취소하는 기능은 의도적으로 지원하지 않습니다.
//   - 따라서 미들웨어에서 Next를 호출하지 않더라도 후속 핸들러와 종단 핸들러는 그대로 실행됩니다.
//     (Next 호출 여부는 "감싸는 위치"만 결정할 뿐, 실행 여부를 결정하지 않습니다.)
//   - 미들웨어는 로깅, 실행 시간 측정, 통계 수집 등 부수 작업 용도로 사용하십시오.
//     연산 자체를 거부해야 한다면 미들웨어가 아니라 호출 측에서 사전 검사하십시오.
//
// [패닉 처리: 실행 단계에 따라 결과가 다름]
// Use로 등록한 미들웨어는 Put 단계와 Pipe 단계 양쪽 체인에 모두 들어가며,
// 두 단계는 서로 다른 고루틴에서 실행됩니다.
//   - Put 단계(ActionPut)는 Put을 호출한 고루틴에서 실행됩니다.
//     여기서 발생한 panic은 호출 측으로 전파되므로 호출 측에서 recover할 수 있습니다.
//   - Pipe 단계(ActionPipe)는 내부 pipe 고루틴에서 실행됩니다.
//     이 고루틴에는 recover 지점이 없으므로 panic이 복구되지 않고 프로세스 전체를 종료시킵니다.
//
// 따라서 미들웨어는 panic이 발생하지 않도록 작성해야 합니다.
// Action()으로 단계를 구분하지 않은 미들웨어는 양쪽 단계에서 모두 실행되므로,
// Put 단계에서 무해해 보이던 코드가 Pipe 단계에서는 프로세스를 내릴 수 있습니다.
func (c *Context[T]) Next() {
	c.index++
	for c.index < len(c.handlers) {
		if c.handlers[c.index] != nil {
			c.handlers[c.index](c)
		}
		c.index++
	}
}

func (c *Context[T]) Action() ActionType { return c.action }
func (c *Context[T]) Data() T            { return c.data }
func (c *Context[T]) Err() error         { return c.err }

func (c *Context[T]) reset() {
	*c = Context[T]{}
}
