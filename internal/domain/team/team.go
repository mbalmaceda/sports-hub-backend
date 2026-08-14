package team

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound            = errors.New("team not found")
	ErrBankAccountNotFound = errors.New("team has no bank account on file")
	ErrNameTaken           = errors.New("team name already taken")
)

type Team struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	SportID  string `json:"sport_id"`
	ClubID   string `json:"club_id,omitempty"`
	Category string `json:"category"`
	// Ciudad y descripción son datos de exhibición: los llena el alta de equipo
	// y los muestran la ficha y el buscador. Nada decide nada con ellos.
	City        string    `json:"city"`
	Description string    `json:"description"`
	LogoURL     string    `json:"logo_url,omitempty"`
	FeeAmount   int64     `json:"fee_amount"`
	FeeDueDay   int       `json:"fee_due_day"`
	Currency    string    `json:"currency"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
}

// BankAccount son los datos a los que los jugadores le transfieren al equipo.
//
// Los campos son genéricos y no chilenos a propósito: HolderTaxID en vez de RUT,
// AccountType como texto libre en vez de un enum con las cuentas de un solo
// país. El mismo modelo tiene que servir para el CUIT argentino y el CPF
// brasileño sin migrar la tabla.
type BankAccount struct {
	TeamID        string    `json:"team_id"`
	BankName      string    `json:"bank_name"`
	AccountType   string    `json:"account_type"`
	AccountNumber string    `json:"account_number"`
	HolderName    string    `json:"holder_name"`
	HolderTaxID   string    `json:"holder_tax_id"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type FeeConfig struct {
	FeeAmount int64
	FeeDueDay int
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*Team, error)
	Create(ctx context.Context, t *Team) error
	List(ctx context.Context) ([]*Team, error)
	UpdateFeeConfig(ctx context.Context, id string, cfg FeeConfig) error
	SearchByName(ctx context.Context, query string) ([]*Team, error)
	GetBankAccount(ctx context.Context, teamID string) (*BankAccount, error)
	SaveBankAccount(ctx context.Context, acc *BankAccount) error
}
