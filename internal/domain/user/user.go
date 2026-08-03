package user

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrNotFound   = errors.New("user not found")
	ErrTaxIDTaken = errors.New("tax id already registered")
)

// Date es una fecha civil (YYYY-MM-DD). Postgres la guarda como DATE y la API
// la expone en formato YYYY-MM-DD, sin zona horaria ni hora.
type Date struct {
	time.Time
}

func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + d.Format("2006-01-02") + `"`), nil
}

func (d *Date) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*d = Date{}
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return fmt.Errorf("invalid date %q, expected YYYY-MM-DD", s)
	}
	*d = Date{Time: t}
	return nil
}

// Scan implementa sql.Scanner para que pgx lea la columna DATE.
func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case time.Time:
		*d = Date{Time: v}
		return nil
	case nil:
		*d = Date{}
		return nil
	default:
		return fmt.Errorf("unsupported Date scan type %T", src)
	}
}

// Value implementa driver.Valuer para que pgx escriba la columna DATE.
func (d Date) Value() (driver.Value, error) {
	if d.IsZero() {
		return nil, nil
	}
	return d.Time, nil
}

// User no tiene rol: el rol es por membership (ver domain/membership),
// porque un usuario puede tener un rol distinto en cada equipo.
type User struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Email         string     `json:"email"`
	TaxID         string     `json:"tax_id,omitempty"`
	Phone         string     `json:"phone,omitempty"`
	AvatarURL     string     `json:"avatar_url,omitempty"`
	FavoriteSport string     `json:"favorite_sport,omitempty"`
	HeightCm      *int       `json:"height_cm,omitempty"`
	WeightKg      *float64   `json:"weight_kg,omitempty"`
	BirthDate     *Date      `json:"birth_date,omitempty"`
	Alias         string     `json:"alias,omitempty"`
	City          string     `json:"city,omitempty"`
	DominantSide  string     `json:"dominant_side,omitempty"`
	Bio           string     `json:"bio,omitempty"`
	PushToken     string     `json:"-"`
	PasswordHash  string     `json:"-"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type ProfileUpdate struct {
	Name          string
	TaxID         string
	Phone         string
	AvatarURL     string
	FavoriteSport string
	HeightCm      *int
	WeightKg      *float64
	BirthDate     *Date
	Alias         string
	City          string
	DominantSide  string
	Bio           string
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	Create(ctx context.Context, u *User) error
	UpdateProfile(ctx context.Context, userID string, update ProfileUpdate) error
	UpdatePushToken(ctx context.Context, userID, token string) error
}
