/*
Package venue es el catálogo de canchas: un nombre y una coordenada.

Existe para poner amistosos en un mapa. Mientras la cancha fue texto libre, el
mismo complejo aparecía escrito de cinco maneras y sin ningún punto donde
dibujarlo; como catálogo, se carga una vez y todas las búsquedas que la usan
caen exactamente en el mismo lugar.

Lo carga el equipo de ZPORTS (verificadas) y lo completan los managers cuando
su cancha no está (sin verificar). Las segundas sirven igual: la alternativa
era que el manager no pudiera publicar hasta que alguien cargara su cancha.
*/
package venue

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
	"unicode"
)

var (
	ErrNotFound = errors.New("venue not found")
	// ErrInvalid cubre nombre vacío o coordenada fuera de rango.
	ErrInvalid = errors.New("venue needs a name and a valid coordinate")
)

// DedupeRadiusKm es a qué distancia dos canchas con el mismo nombre se
// consideran la misma. 300 m alcanza para absorber dos pines puestos a ojo
// sobre el mismo complejo, y deja como distintas a dos "Cancha 1" de comunas
// vecinas.
const DedupeRadiusKm = 0.3

type Venue struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	Verified bool    `json:"verified"`
	// RejectedAt es cuándo el admin la sacó del catálogo. Nula es viva.
	RejectedAt *time.Time `json:"rejected_at,omitempty"`
	// DistanceKm solo viene en las búsquedas con punto de referencia.
	DistanceKm *float64  `json:"distance_km,omitempty"`
	CreatedBy  *string   `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// IsRejected dice si el admin la sacó del catálogo: no se puede volver a elegir.
func (v *Venue) IsRejected() bool {
	return v.RejectedAt != nil
}

// Validate revisa lo mínimo para que la cancha se pueda dibujar.
func (v *Venue) Validate() error {
	if strings.TrimSpace(v.Name) == "" || !ValidCoordinate(v.Lat, v.Lng) {
		return ErrInvalid
	}
	return nil
}

// ValidCoordinate rechaza lo que no es un punto de la Tierra, y también el
// (0, 0): es lo que manda un cliente que no tenía coordenada y la dejó en cero,
// y en el golfo de Guinea no hay canchas.
func ValidCoordinate(lat, lng float64) bool {
	if math.IsNaN(lat) || math.IsNaN(lng) {
		return false
	}
	if lat == 0 && lng == 0 {
		return false
	}
	return lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
}

var accents = strings.NewReplacer(
	"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n",
	"à", "a", "è", "e", "ì", "i", "ò", "o", "ù", "u",
)

/*
NameKey es el nombre como se compara: minúsculas, sin tildes y con los
espacios colapsados.

Va en Go y no con `unaccent` en Postgres porque esa extensión no está
habilitada, y porque la misma regla la usan la búsqueda y la deduplicación: dos
implementaciones del mismo criterio terminan discrepando en una tilde.
*/
func NameKey(name string) string {
	lowered := accents.Replace(strings.ToLower(name))
	var b strings.Builder
	space := false
	for _, r := range lowered {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

/*
Update son los campos que el admin puede corregir. Nil es "no tocar".

`Rejected` en true la saca del catálogo y le quita la verificación; en false la
restaura. Aprobar (`Verified` en true) también la restaura: una cancha no puede
estar verificada y rechazada a la vez.
*/
type Update struct {
	Name     *string  `json:"name"`
	Lat      *float64 `json:"lat"`
	Lng      *float64 `json:"lng"`
	Verified *bool    `json:"verified"`
	Rejected *bool    `json:"rejected"`
}

// Status es en qué cola del admin está una cancha. Se deriva de `verified` y
// `rejected_at`: no hay columna propia que pueda discrepar con esas dos.
type Status string

const (
	StatusPending  Status = "pending"
	StatusVerified Status = "verified"
	StatusRejected Status = "rejected"
)

// ValidStatus acepta los tres estados y el vacío, que es "todas".
func ValidStatus(s Status) bool {
	return s == "" || s == StatusPending || s == StatusVerified || s == StatusRejected
}

// Filter acota el listado del admin.
type Filter struct {
	Query  string
	Status Status
}

/*
AdminVenue es una cancha con lo que hace falta para decidir sobre ella: cuánto
se usa y quién la propuso. Una sugerencia con tres partidos abiertos no se
rechaza igual que una que nadie usó.
*/
type AdminVenue struct {
	Venue
	// OpenSearches son las búsquedas abiertas y sin vencer en esta cancha.
	OpenSearches int `json:"open_searches"`
	// Competitions son las competencias que la eligieron, de cualquier estado.
	Competitions int `json:"competitions"`
	// SuggestedBy es el nombre de quien la propuso; vacío si la cargó el admin.
	SuggestedBy string `json:"suggested_by,omitempty"`
}

// NearbyRadiusKm es hasta dónde se buscan candidatas para fusionar: un
// duplicado es un segundo pin sobre el mismo complejo, no el de la otra cuadra.
const NearbyRadiusKm = 1.0

type Repository interface {
	FindByID(ctx context.Context, id string) (*Venue, error)
	/*
		Search es el autocompletado: canchas cuyo nombre contiene `query`,
		primero las verificadas y después las más cercanas al punto, si viene.

		Sin punto ordena por nombre. Contiene y no "empieza con" porque la gente
		escribe "maipu" buscando "Complejo Deportivo Maipú".
	*/
	Search(ctx context.Context, query string, lat, lng *float64, limit int) ([]*Venue, error)
	/*
		FindOrCreate devuelve la cancha con el mismo `NameKey` a menos de
		`DedupeRadiusKm`, o la crea. Es lo que hace que un doble toque, o dos
		managers sugiriendo el mismo complejo, no dejen dos pines.

		El bool dice si la creó.
	*/
	FindOrCreate(ctx context.Context, v *Venue) (*Venue, bool, error)

	// ── Admin ──

	List(ctx context.Context, f Filter) ([]*AdminVenue, error)
	// Nearby son las canchas vivas a menos de `radiusKm` de `id`, sin ella:
	// las candidatas para fusionar un duplicado.
	Nearby(ctx context.Context, id string, radiusKm float64) ([]*Venue, error)
	Create(ctx context.Context, v *Venue) error
	Update(ctx context.Context, id string, u Update) (*Venue, error)
	// Merge mueve todo lo que apuntaba a `fromID` hacia `intoID` y borra la
	// de origen, en una transacción. Es la salida para el duplicado que
	// sugirió un manager sin ver que la cancha ya estaba.
	Merge(ctx context.Context, fromID, intoID string) error
}
