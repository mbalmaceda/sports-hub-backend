package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/mbalmaceda/sports-hub-backend/internal/config"
	"github.com/mbalmaceda/sports-hub-backend/internal/db"
)

const defaultPassword = "password123"

type dummyUser struct {
	Name     string
	Email    string
	Phone    string
	Avatar   string
	Role     string
	Jersey   int
	Position string
}

type dummyTeam struct {
	Name      string
	SportID   string
	Category  string
	FeeAmount int64
	FeeDueDay int
	Currency  string
	Roster    []dummyUser
}

var teams = []dummyTeam{
	{
		Name: "Deportivo Norte", SportID: "football", Category: "Senior",
		FeeAmount: 15000, FeeDueDay: 10, Currency: "CLP",
		Roster: []dummyUser{
			{Name: "Mirko Balmaceda", Email: "mirko@test.com", Phone: "+56912345678", Role: "manager", Jersey: 10, Position: "Volante"},
			{Name: "Test User", Email: "test2@test.com", Role: "treasurer", Jersey: 12, Position: "Arquero"},
			{Name: "Carlos Pérez", Email: "carlos@test.com", Phone: "+56910000001", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=carlos", Role: "player", Jersey: 1, Position: "Arquero"},
			{Name: "Luis González", Email: "luis@test.com", Phone: "+56910000002", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=luis", Role: "player", Jersey: 2, Position: "Defensa"},
			{Name: "Andrés Díaz", Email: "andres@test.com", Phone: "+56910000003", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=andres", Role: "player", Jersey: 3, Position: "Defensa"},
			{Name: "Marco Torres", Email: "marco@test.com", Phone: "+56910000004", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=marco", Role: "player", Jersey: 4, Position: "Defensa"},
			{Name: "Diego Rojas", Email: "diego@test.com", Phone: "+56910000005", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=diego", Role: "player", Jersey: 5, Position: "Volante"},
			{Name: "Felipe Soto", Email: "felipe@test.com", Phone: "+56910000006", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=felipe", Role: "player", Jersey: 6, Position: "Volante"},
			{Name: "Nicolás Vega", Email: "nicolas@test.com", Phone: "+56910000007", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=nicolas", Role: "player", Jersey: 8, Position: "Volante"},
			{Name: "Javier Morales", Email: "javier@test.com", Phone: "+56910000008", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=javier", Role: "player", Jersey: 7, Position: "Delantero"},
			{Name: "Sebastián Castro", Email: "sebastian@test.com", Phone: "+56910000009", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=sebastian", Role: "player", Jersey: 9, Position: "Delantero"},
			{Name: "Matías Fuentes", Email: "matias@test.com", Phone: "+56910000010", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=matias", Role: "player", Jersey: 11, Position: "Delantero"},
		},
	},
	{
		Name: "Los Rayos", SportID: "football", Category: "Juvenil",
		FeeAmount: 10000, FeeDueDay: 5, Currency: "CLP",
		Roster: []dummyUser{
			{Name: "Ignacio Rojas", Email: "ignacio@test.com", Phone: "+56910000011", Role: "manager", Jersey: 10, Position: "Volante"},
			{Name: "Tomás Herrera", Email: "tomas@test.com", Phone: "+56910000012", Role: "treasurer", Jersey: 1, Position: "Arquero"},
			{Name: "Cristóbal Araya", Email: "cristobal@test.com", Phone: "+56910000013", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=cristobal", Role: "player", Jersey: 1, Position: "Arquero"},
			{Name: "Benjamín Espinoza", Email: "benjamin@test.com", Phone: "+56910000014", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=benjamin", Role: "player", Jersey: 2, Position: "Defensa"},
			{Name: "Vicente Salazar", Email: "vicente@test.com", Phone: "+56910000015", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=vicente", Role: "player", Jersey: 3, Position: "Defensa"},
			{Name: "Agustín Morales", Email: "agustin@test.com", Phone: "+56910000016", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=agustin", Role: "player", Jersey: 4, Position: "Defensa"},
			{Name: "Maximiliano Salas", Email: "maximiliano@test.com", Phone: "+56910000017", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=maximiliano", Role: "player", Jersey: 5, Position: "Volante"},
			{Name: "Joaquín Paredes", Email: "joaquin@test.com", Phone: "+56910000018", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=joaquin", Role: "player", Jersey: 6, Position: "Volante"},
			{Name: "Renato López", Email: "renato@test.com", Phone: "+56910000019", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=renato", Role: "player", Jersey: 7, Position: "Volante"},
			{Name: "Sebastián Muñoz", Email: "sebastian.munoz@test.com", Phone: "+56910000020", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=sebastianmunoz", Role: "player", Jersey: 8, Position: "Delantero"},
			{Name: "Martín Carrasco", Email: "martin@test.com", Phone: "+56910000021", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=martin", Role: "player", Jersey: 9, Position: "Delantero"},
			{Name: "Thiago Gutiérrez", Email: "thiago@test.com", Phone: "+56910000022", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=thiago", Role: "player", Jersey: 11, Position: "Delantero"},
		},
	},
	{
		Name: "Atlético Centro", SportID: "basketball", Category: "Senior",
		FeeAmount: 20000, FeeDueDay: 15, Currency: "CLP",
		Roster: []dummyUser{
			{Name: "Rodrigo Campos", Email: "rodrigo@test.com", Phone: "+56910000023", Role: "manager", Jersey: 10, Position: "Alero"},
			{Name: "Paula Méndez", Email: "paula@test.com", Phone: "+56910000024", Role: "treasurer", Jersey: 11, Position: "Base"},
			{Name: "Javier Silva", Email: "javier.silva@test.com", Phone: "+56910000025", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=javiersilva", Role: "player", Jersey: 4, Position: "Base"},
			{Name: "Nicolás Riquelme", Email: "nicolas.riquelme@test.com", Phone: "+56910000026", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=nicolasriquelme", Role: "player", Jersey: 5, Position: "Base"},
			{Name: "Cristian Vidal", Email: "cristian@test.com", Phone: "+56910000027", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=cristian", Role: "player", Jersey: 6, Position: "Escolta"},
			{Name: "Matías Orellana", Email: "matias.orellana@test.com", Phone: "+56910000028", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=matiasorellana", Role: "player", Jersey: 7, Position: "Escolta"},
			{Name: "Felipe Contreras", Email: "felipe.contreras@test.com", Phone: "+56910000029", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=felipecontreras", Role: "player", Jersey: 8, Position: "Alero"},
			{Name: "Gabriel Sandoval", Email: "gabriel@test.com", Phone: "+56910000030", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=gabriel", Role: "player", Jersey: 9, Position: "Alero"},
			{Name: "Álvaro Reyes", Email: "alvaro@test.com", Phone: "+56910000031", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=alvaro", Role: "player", Jersey: 12, Position: "Ala-pívot"},
			{Name: "Diego Pizarro", Email: "diego.pizarro@test.com", Phone: "+56910000032", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=diegopizarro", Role: "player", Jersey: 13, Position: "Ala-pívot"},
			{Name: "Ricardo Navarro", Email: "ricardo@test.com", Phone: "+56910000033", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=ricardo", Role: "player", Jersey: 14, Position: "Pívot"},
			{Name: "Óscar Fuentealba", Email: "oscar@test.com", Phone: "+56910000034", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=oscar", Role: "player", Jersey: 15, Position: "Pívot"},
		},
	},
	{
		Name: "Volley Las Condes", SportID: "volleyball", Category: "Damas",
		FeeAmount: 12000, FeeDueDay: 20, Currency: "CLP",
		Roster: []dummyUser{
			{Name: "Camila Fernández", Email: "camila@test.com", Phone: "+56910000035", Role: "manager", Jersey: 5, Position: "Punta"},
			{Name: "Valentina Ríos", Email: "valentina@test.com", Phone: "+56910000036", Role: "treasurer", Jersey: 6, Position: "Opuesta"},
			{Name: "Fernanda Cortés", Email: "fernanda@test.com", Phone: "+56910000037", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=fernanda", Role: "player", Jersey: 1, Position: "Armadora"},
			{Name: "Antonia Jara", Email: "antonia@test.com", Phone: "+56910000038", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=antonia", Role: "player", Jersey: 2, Position: "Armadora"},
			{Name: "Josefina Tapia", Email: "josefina@test.com", Phone: "+56910000039", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=josefina", Role: "player", Jersey: 3, Position: "Punta"},
			{Name: "Catalina Bravo", Email: "catalina@test.com", Phone: "+56910000040", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=catalina", Role: "player", Jersey: 4, Position: "Punta"},
			{Name: "Isidora Pino", Email: "isidora@test.com", Phone: "+56910000041", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=isidora", Role: "player", Jersey: 7, Position: "Punta"},
			{Name: "Constanza Vera", Email: "constanza@test.com", Phone: "+56910000042", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=constanza", Role: "player", Jersey: 8, Position: "Opuesta"},
			{Name: "Belén Aguilar", Email: "belen@test.com", Phone: "+56910000043", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=belen", Role: "player", Jersey: 9, Position: "Central"},
			{Name: "Amanda Castro", Email: "amanda@test.com", Phone: "+56910000044", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=amanda", Role: "player", Jersey: 10, Position: "Central"},
			{Name: "Renata Figueroa", Email: "renata@test.com", Phone: "+56910000045", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=renata", Role: "player", Jersey: 11, Position: "Central"},
			{Name: "Victoria Rojas", Email: "victoria@test.com", Phone: "+56910000046", Avatar: "https://api.dicebear.com/9.x/personas/png?seed=victoria", Role: "player", Jersey: 12, Position: "Líbero"},
		},
	},
}

func main() {
	months := flag.Int("months", 3, "cantidad de meses de cuotas a generar (historial hacia atrás)")
	flag.Parse()

	config.LoadDotEnv()

	// Este comando crea usuarios con una contraseña conocida y publicada en este
	// mismo archivo. Corrido contra una base compartida, esas cuentas quedan a
	// disposición de cualquiera que lea el repositorio. El opt-in explícito
	// existe para que sea imposible hacerlo por accidente, apuntando el
	// DATABASE_URL equivocado.
	if os.Getenv("ALLOW_SEED") != "true" {
		log.Fatal("seed deshabilitado: exporta ALLOW_SEED=true y verifica a qué base apunta DATABASE_URL")
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	hash, err := bcrypt.GenerateFromPassword([]byte(defaultPassword), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal(err)
	}

	createdUsers, createdMemberships := 0, 0
	feesCreated, paymentsCreated := 0, 0

	for _, team := range teams {
		teamID, err := ensureTeam(ctx, pool, team)
		if err != nil {
			log.Fatal(err)
		}

		memberships := make([]struct {
			ID     string
			UserID string
		}, 0, len(team.Roster))

		for _, m := range team.Roster {
			userID, created, err := ensureUser(ctx, pool, m, string(hash))
			if err != nil {
				log.Fatal(err)
			}
			if created {
				createdUsers++
			}

			membershipID, created, err := ensureMembership(ctx, pool, userID, teamID, m)
			if err != nil {
				log.Fatal(err)
			}
			if created {
				createdMemberships++
			}
			memberships = append(memberships, struct {
				ID     string
				UserID string
			}{membershipID, userID})
		}

		managerID := memberships[0].UserID
		fees, payments, err := seedFees(ctx, pool, teamID, managerID, memberships, *months)
		if err != nil {
			log.Fatal(err)
		}
		feesCreated += fees
		paymentsCreated += payments

		slog.Info("team ready", "name", team.Name, "team_id", teamID, "members", len(memberships))
	}

	slog.Info("done",
		"created_users", createdUsers,
		"created_memberships", createdMemberships,
		"fee_obligations_created", feesCreated,
		"payments_created", paymentsCreated,
		"password", defaultPassword,
	)
}

func ensureTeam(ctx context.Context, pool *pgxpool.Pool, t dummyTeam) (string, error) {
	const q = `
		INSERT INTO teams (name, sport_id, category, fee_amount, fee_due_day, currency, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, true)
		ON CONFLICT DO NOTHING`
	if _, err := pool.Exec(ctx, q, t.Name, t.SportID, t.Category, t.FeeAmount, t.FeeDueDay, t.Currency); err != nil {
		return "", fmt.Errorf("ensureTeam %s: %w", t.Name, err)
	}
	var id string
	if err := pool.QueryRow(ctx, `SELECT id FROM teams WHERE name = $1`, t.Name).Scan(&id); err != nil {
		return "", fmt.Errorf("ensureTeam %s: find: %w", t.Name, err)
	}
	return id, nil
}

func ensureUser(ctx context.Context, pool *pgxpool.Pool, u dummyUser, passwordHash string) (string, bool, error) {
	var id string
	err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, u.Email).Scan(&id)
	if err == nil {
		return id, false, nil
	}

	const q = `
		INSERT INTO users (name, email, password_hash, phone, avatar_url)
		VALUES ($1, $2, $3, NULLIF($4,''), NULLIF($5,''))
		RETURNING id`
	if err := pool.QueryRow(ctx, q, u.Name, u.Email, passwordHash, u.Phone, u.Avatar).Scan(&id); err != nil {
		return "", false, fmt.Errorf("ensureUser %s: %w", u.Email, err)
	}
	return id, true, nil
}

func ensureMembership(ctx context.Context, pool *pgxpool.Pool, userID, teamID string, u dummyUser) (string, bool, error) {
	const q = `
		INSERT INTO memberships (user_id, team_id, role, status, jersey_number, position)
		VALUES ($1, $2, $3, 'active', $4, NULLIF($5,''))
		ON CONFLICT (user_id, team_id) DO UPDATE SET
			role = EXCLUDED.role,
			status = EXCLUDED.status,
			jersey_number = EXCLUDED.jersey_number,
			position = EXCLUDED.position
		RETURNING id, (xmax = 0) AS inserted`
	var id string
	var inserted bool
	if err := pool.QueryRow(ctx, q, userID, teamID, u.Role, u.Jersey, u.Position).Scan(&id, &inserted); err != nil {
		return "", false, fmt.Errorf("ensureMembership %s: %w", u.Email, err)
	}
	return id, inserted, nil
}

// seedFees genera cuotas para los últimos `months` meses. Idempotente:
// se salta obligaciones ya existentes (UNIQUE membership+period) y pagos ya registrados.
func seedFees(ctx context.Context, pool *pgxpool.Pool, teamID, managerID string, memberships []struct {
	ID     string
	UserID string
}, months int) (int, int, error) {
	var feeAmount int64
	var currency string
	var dueDay int
	if err := pool.QueryRow(ctx,
		`SELECT fee_amount, currency, fee_due_day FROM teams WHERE id = $1`, teamID,
	).Scan(&feeAmount, &currency, &dueDay); err != nil {
		return 0, 0, fmt.Errorf("seedFees: team: %w", err)
	}

	now := time.Now()
	methods := []string{"transfer", "cash", "online"}
	feesCreated, paymentsCreated := 0, 0

	for offset := months - 1; offset >= 0; offset-- {
		d := now.AddDate(0, -offset, 0)
		year, month := d.Year(), int(d.Month())
		dueDate := time.Date(year, time.Month(month), dueDay, 12, 0, 0, 0, time.UTC)

		for idx, m := range memberships {
			status, paidAt := "paid", dueDate.Add(24*time.Hour)
			switch {
			case offset >= 2:
				// histórico: todo pagado
			case offset == 1 && idx == 3:
				status, paidAt = "overdue", time.Time{}
			case offset == 1 && idx == 5:
				status, paidAt = "pending", time.Time{}
			case offset == 0 && idx%2 == 1:
				status, paidAt = "pending", time.Time{}
			}

			tag, err := pool.Exec(ctx, `
				INSERT INTO fee_obligations
					(team_id, membership_id, period_year, period_month, amount, currency, due_date, status, paid_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				ON CONFLICT (membership_id, period_year, period_month) DO NOTHING`,
				teamID, m.ID, year, month, feeAmount, currency, dueDate, status,
				nullableTime(paidAt),
			)
			if err != nil {
				return 0, 0, fmt.Errorf("seedFees: obligation %d/%d: %w", year, month, err)
			}
			if tag.RowsAffected() == 0 {
				continue
			}
			feesCreated++

			if status != "paid" {
				continue
			}

			var obligationID string
			if err := pool.QueryRow(ctx, `
				SELECT id FROM fee_obligations
				WHERE membership_id = $1 AND period_year = $2 AND period_month = $3`,
				m.ID, year, month,
			).Scan(&obligationID); err != nil {
				return 0, 0, fmt.Errorf("seedFees: find obligation: %w", err)
			}

			tag, err = pool.Exec(ctx, `
				INSERT INTO payments
					(team_id, obligation_id, payer_id, recorded_by, amount, currency, method, notes)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
				ON CONFLICT DO NOTHING`,
				teamID, obligationID, m.UserID, managerID, feeAmount, currency,
				methods[idx%len(methods)],
				fmt.Sprintf("Cuota %s %d", time.Month(month).String(), year),
			)
			if err != nil {
				return 0, 0, fmt.Errorf("seedFees: payment %d/%d: %w", year, month, err)
			}
			paymentsCreated += int(tag.RowsAffected())
		}
	}

	return feesCreated, paymentsCreated, nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
