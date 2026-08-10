package testutil

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/expense"
)

// --- ExpenseRepository ---

type MockExpenseRepo struct{ mock.Mock }

func (m *MockExpenseRepo) Create(ctx context.Context, input expense.CreateInput) (*expense.Expense, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*expense.Expense), args.Error(1)
}

func (m *MockExpenseRepo) ListByTeamAndPeriod(
	ctx context.Context, teamID string, year, month int,
) ([]*expense.Expense, error) {
	args := m.Called(ctx, teamID, year, month)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*expense.Expense), args.Error(1)
}

func (m *MockExpenseRepo) ListBySource(ctx context.Context, source expense.Source) ([]*expense.Expense, error) {
	args := m.Called(ctx, source)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*expense.Expense), args.Error(1)
}

func (m *MockExpenseRepo) GetByID(ctx context.Context, id string) (*expense.Expense, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*expense.Expense), args.Error(1)
}

func (m *MockExpenseRepo) Delete(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}
