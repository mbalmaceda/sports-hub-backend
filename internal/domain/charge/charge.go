package charge

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("charge not found")
	// ErrNotPayable cubre subir comprobante a un cargo ya pagado o ya enviado.
	ErrNotPayable = errors.New("this charge is not awaiting payment")
	// ErrNotSubmitted protege de confirmar un cargo del que nadie subió nada.
	ErrNotSubmitted = errors.New("this charge has no receipt to review")
)

type SourceType string

const (
	SourceMonthlyFee SourceType = "monthly_fee"
	SourceMatchCost  SourceType = "match_cost"
)

// Source es de dónde sale el cargo: una cuota mensual o el costo de una
// competencia. El par (tipo, id) apunta a tablas distintas según el tipo, por
// eso no es una llave foránea.
type Source struct {
	Type SourceType `json:"type"`
	ID   string     `json:"id"`
}

type Status string

const (
	StatusPending Status = "pending"
	// StatusSubmitted es del flujo viejo, cuando el comprobante esperaba que un
	// manager lo confirmara. Ya nadie lo produce: subir el comprobante deja el
	// cargo en 'paid' directo (ver SubmitReceipt). Se conserva porque el
	// esquema todavía lo acepta y porque un rollback del backend lo revive.
	StatusSubmitted Status = "submitted"
	StatusPaid      Status = "paid"
	StatusWaived    Status = "waived"
)

// IsSettled indica si el cargo ya no admite que se rehaga el reparto: es plata
// que alguien ya declaró como transferida. Rehacer sobre eso la borraría.
func (s Status) IsSettled() bool {
	return s == StatusPaid || s == StatusSubmitted
}

type Charge struct {
	ID           string     `json:"id"`
	TeamID       string     `json:"team_id"`
	MembershipID string     `json:"membership_id"`
	Source       Source     `json:"source"`
	Amount       int64      `json:"amount"`
	Currency     string     `json:"currency"`
	Status       Status     `json:"status"`
	DueDate      *time.Time `json:"due_date,omitempty"`
	ReceiptURL   string     `json:"receipt_url,omitempty"`
	SubmittedAt  *time.Time `json:"submitted_at,omitempty"`
	ConfirmedAt  *time.Time `json:"confirmed_at,omitempty"`
	ConfirmedBy  *string    `json:"confirmed_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// Line es un renglón del reparto: a quién y cuánto.
type Line struct {
	MembershipID string `json:"membership_id"`
	Amount       int64  `json:"amount"`
}

type CreateInput struct {
	TeamID   string
	Source   Source
	Currency string
	DueDate  *time.Time
	Lines    []Line
}

// EnsureInput crea el cargo de UNA membresía sin tocar los del resto.
//
// Existe aparte de CreateInput porque los dos momentos son distintos: el
// reparto del manager rehace el conjunto entero, mientras que un jugador que
// acepta la citación solo agrega el suyo. Usar el reparto para eso borraría
// los cargos pendientes de sus compañeros.
type EnsureInput struct {
	TeamID       string
	MembershipID string
	Source       Source
	Amount       int64
	Currency     string
}

// MatchCostSplit es el reparto del costo de un lugar entre los jugadores que van.
type MatchCostSplit struct {
	Lines []Line
	// PerPlayer es la cuota fija por cabeza. Sale de la nómina, no de cuántos
	// confirmaron: con un lugar de $28.000 y 7 por equipo son $2.000 siempre.
	PerPlayer int64
	// TeamShare es la mitad del lugar, lo que le toca a nuestro equipo.
	TeamShare int64
	// Surplus es lo recaudado por encima de esa mitad. Con más confirmados que
	// la nómina (los cambios) sobra plata, y queda en los fondos del equipo.
	Surplus int64
}

// ceilDiv divide redondeando hacia arriba, sin pasar por float: en dinero un
// error de redondeo por punto flotante se convierte en pesos que no cuadran.
func ceilDiv(total int64, parts int64) int64 {
	if parts <= 0 {
		return 0
	}
	return (total + parts - 1) / parts
}

/*
SplitMatchCost reparte el costo del lugar entre los jugadores que van.

Es el espejo exacto de `splitMatchCost` del mobile, y existe del lado del
servidor para no depender de que el cliente calcule bien: los montos que llegan
por la red no se usan, se recalculan acá. Un cliente manipulado no puede
generar deuda a su gusto.

El precio por cabeza es fijo —lugar ÷ (jugadores por equipo × 2)— porque cada
equipo cubre la mitad del lugar. Si confirman más que la nómina, lo recaudado
pasa esa mitad y el excedente queda para el equipo.
*/
// PerPlayerShare es la cuota fija por jugador: el lugar dividido entre los que
// entran en cancha por los dos lados.
//
// Queda determinada al crear la competencia y no depende de cuántos confirmen:
// con un lugar de $28.000 y 7 por equipo son $2.000, vayan 8 o 14. Por eso se
// le puede cobrar a un jugador apenas acepta la citación, sin esperar a que el
// manager reparta. Devuelve 0 si falta el costo o la nómina.
func PerPlayerShare(totalAmount int64, playersPerSide int) int64 {
	if totalAmount <= 0 || playersPerSide <= 0 {
		return 0
	}
	return ceilDiv(totalAmount, int64(playersPerSide)*2)
}

func SplitMatchCost(totalAmount int64, playersPerSide int, payerMembershipIDs []string) MatchCostSplit {
	return SplitMatchCostWithShare(totalAmount, PerPlayerShare(totalAmount, playersPerSide), payerMembershipIDs)
}

// SplitMatchCostWithShare reparte usando la cuota que ya está guardada en la
// competencia, en vez de volver a derivarla del costo y la nómina.
//
// Es la que usa el reparto del manager: el número se fijó al crear la
// competencia y es el que se le viene cobrando a quienes aceptaron antes.
// Recalcularlo abriría la puerta a que dos jugadores del mismo partido terminen
// con montos distintos si la fórmula cambia entremedio.
func SplitMatchCostWithShare(totalAmount, perPlayer int64, payerMembershipIDs []string) MatchCostSplit {
	teamShare := ceilDiv(totalAmount, 2)

	if len(payerMembershipIDs) == 0 || totalAmount <= 0 || perPlayer <= 0 {
		return MatchCostSplit{Lines: []Line{}, TeamShare: teamShare}
	}

	lines := make([]Line, 0, len(payerMembershipIDs))
	for _, id := range payerMembershipIDs {
		lines = append(lines, Line{MembershipID: id, Amount: perPlayer})
	}

	return MatchCostSplit{
		Lines:     lines,
		PerPlayer: perPlayer,
		TeamShare: teamShare,
		Surplus:   perPlayer*int64(len(payerMembershipIDs)) - teamShare,
	}
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*Charge, error)
	ListBySource(ctx context.Context, source Source) ([]*Charge, error)
	ListByMembership(ctx context.Context, membershipID string) ([]*Charge, error)
	/*
		CreateForSource reemplaza los cargos pendientes de ese origen y conserva
		los que ya están cobrados o con comprobante enviado.

		Es lo que hace seguro rehacer el reparto cuando cambia la nómina: se
		recalcula lo que nadie tocó y no se pisa lo que ya se pagó.
	*/
	CreateForSource(ctx context.Context, in CreateInput) ([]*Charge, error)
	/*
		EnsureForMembership deja creado el cargo de una sola membresía.

		Es idempotente y no pisa nada: si ya existe —pendiente, enviado o
		pagado— devuelve el que está. Sirve para el jugador que acepta la
		citación y puede pagar en el acto, sin esperar el reparto del manager.
	*/
	EnsureForMembership(ctx context.Context, in EnsureInput) (*Charge, error)
	/*
		RemovePendingForMembership borra el cargo pendiente de una membresía.

		Es lo que corresponde cuando alguien que había aceptado avisa que ya no
		va: cobrarle la cancha a quien no juega rompe la confianza en la app.
		Un cargo ya enviado o pagado no se toca —esa plata existe— y ahí no
		borra nada.
	*/
	RemovePendingForMembership(ctx context.Context, source Source, membershipID string) error
	/*
		SubmitReceipt marca el cargo como pagado con el comprobante del deudor.

		Es un solo paso a propósito: el que transfiere declara y se le cree. El
		control de dos ojos que justificaba el estado intermedio costaba más de
		lo que cuidaba —cobros que quedaban semanas en 'submitted' porque nadie
		revisó— en un equipo donde todos se conocen.

		Deja `confirmed_by` vacío: nadie verificó nada, y así se distingue de lo
		que sí confirmó un tesorero antes de este cambio.
	*/
	SubmitReceipt(ctx context.Context, id, receiptURL string, at time.Time) (*Charge, error)
	// Confirm y RejectReceipt son del flujo viejo y hoy no los llama la app:
	// solo operan sobre 'submitted', que ya nadie produce. Siguen acá porque un
	// rollback del backend vuelve a generar ese estado y alguien tiene que
	// poder resolverlo.
	Confirm(ctx context.Context, id, confirmedBy string, at time.Time) (*Charge, error)
	// RejectReceipt devuelve el cargo a pendiente para que se vuelva a subir.
	RejectReceipt(ctx context.Context, id string) (*Charge, error)
}
