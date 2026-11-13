package llm

import (
	"context"
	"errors"
	"fmt"
)

// ErrRunBudget is a host admission limit, not a provider/network failure.
var ErrRunBudget = errors.New("本轮模型请求或 Token 总预算已用尽，请检查已保存的结果")

type RunBudgetError struct {
	Kind        string
	Used, Limit int64
	Settlement  bool
}

func (e *RunBudgetError) Error() string {
	phase := "本轮执行"
	if e.Settlement {
		phase = "收尾阶段"
	}
	return fmt.Sprintf("%s的%s预算已用尽（已用 %d，上限 %d）；已保存的结果仍保留", phase, e.Kind, e.Used, e.Limit)
}
func (e *RunBudgetError) Unwrap() error { return ErrRunBudget }

type budgetSettlementKey struct{}

// WithBudgetSettlement marks the bounded harness settlement phase. A host may
// provide a separate small reserve; it must not reset the normal run budget.
func WithBudgetSettlement(ctx context.Context) context.Context {
	return context.WithValue(ctx, budgetSettlementKey{}, true)
}
func IsBudgetSettlement(ctx context.Context) bool {
	value, _ := ctx.Value(budgetSettlementKey{}).(bool)
	return value
}
