# Sports Hub API

**Base URL producción:** `https://sports-hub-backend.fly.dev`  
**Base URL local:** `http://localhost:8085`

Todos los endpoints protegidos requieren header:
```
Authorization: Bearer <access_token>
```

---

## Auth

### POST `/auth/register`
Registra un usuario nuevo y devuelve tokens. Los campos del perfil deportivo
son opcionales: la cuenta se crea igual sin ellos.

`birth_date` va en formato `YYYY-MM-DD`. El RUT (`tax_id`) se normaliza
(sin puntos ni guiones, la K en mayúscula) antes de guardarse; intentar
registrar un RUT ya existente devuelve **409**.

`tax_id` es opcional, pero si viene se valida el dígito verificador con el
módulo 11 chileno: uno inventado devuelve **400**. La app móvil además lo exige
para crear la cuenta, porque es la llave con la que un manager encuentra a un
jugador para invitarlo.

**Body**
```json
{
  "name": "Mirko Balmaceda",
  "email": "mirko@example.com",
  "password": "minimo6chars",
  "tax_id": "12.345.678-5",
  "favorite_sport": "football",
  "height_cm": 175,
  "weight_kg": 70.5,
  "birth_date": "1998-07-12",
  "alias": "Miri",
  "city": "Santiago",
  "dominant_side": "right",
  "bio": "Mediocampista"
}
```

**Response 201**
```json
{
  "access_token": "eyJ...",
  "refresh_token": "5e7a...",
  "user": {
    "id": "uuid",
    "name": "Mirko Balmaceda",
    "email": "mirko@example.com",
    "tax_id": "123456789",
    "favorite_sport": "football",
    "height_cm": 175,
    "weight_kg": 70.5,
    "birth_date": "1998-07-12",
    "alias": "Miri",
    "city": "Santiago",
    "dominant_side": "right",
    "bio": "Mediocampista",
    "created_at": "2026-07-18T00:00:00Z",
    "updated_at": "2026-07-18T00:00:00Z"
  }
}
```

**Response 400** `{ "error": "tax_id is not a valid RUT" }`  
**Response 409** `{ "error": "email already registered" }` |
`{ "error": "tax id already registered" }`

---

### POST `/auth/login`
**Body**
```json
{ "email": "mirko@example.com", "password": "minimo6chars" }
```

**Response 200** — igual que register  
**Response 401** `{ "error": "invalid credentials" }`

---

### POST `/auth/refresh`
El refresh token se **rota** en cada uso.

**Body**
```json
{ "refresh_token": "5e7a..." }
```

**Response 200**
```json
{
  "access_token": "eyJ...",
  "refresh_token": "nuevo_token..."
}
```

---

### POST `/auth/logout`
Invalida el refresh token en la DB.

**Body**
```json
{ "refresh_token": "5e7a..." }
```

**Response 200** `{ "status": "ok" }`

---

## Teams 🔒

### GET `/teams`
Lista todos los equipos activos.

**Response 200**
```json
[
  {
    "id": "uuid",
    "name": "Deportivo Norte",
    "sport_id": "football",
    "category": "Senior",
    "club_id": "",
    "logo_url": "",
    "fee_amount": 10000,
    "fee_due_day": 5,
    "currency": "CLP",
    "is_active": true,
    "created_at": "2026-07-18T00:00:00Z"
  }
]
```

---

### POST `/teams`
**Body**
```json
{
  "name": "Deportivo Norte",
  "sport_id": "football",
  "category": "Senior",
  "club_id": "",
  "logo_url": "",
  "fee_amount": 10000,
  "fee_due_day": 5,
  "currency": "CLP"
}
```

**Response 201** — objeto Team  
**Response 409** `{ "error": "team name already taken" }` — el nombre ya existe (sin distinguir mayúsculas)

---

### GET `/teams/:id`
**Response 200** — objeto Team  
**Response 404** `{ "error": "team not found" }`

---

### PATCH `/teams/:id/fee-config`
**Body**
```json
{ "fee_amount": 15000, "fee_due_day": 10 }
```

**Response 200** — objeto Team actualizado

---

## Roster 🔒

### GET `/teams/:id/roster`
Lista todos los miembros del equipo. Combina datos de `memberships` + `users`.

**Response 200**
```json
[
  {
    "membership_id": "uuid",
    "user_id": "uuid",
    "team_id": "uuid",
    "full_name": "Mirko Balmaceda",
    "avatar_url": "",
    "email": "mirko@example.com",
    "phone": "",
    "role": "player",
    "jersey_number": 10,
    "position": "Delantero",
    "status": "active",
    "joined_at": "2026-07-18T00:00:00Z"
  }
]
```

---

### POST `/teams/:id/roster`
Agrega un usuario existente al equipo.

**Body**
```json
{
  "user_id": "uuid",
  "jersey_number": 10,
  "position": "Delantero"
}
```

**Response 201** — objeto TeamMember  
**Response 409** `{ "error": "user is already a member of this team" }`

---

### GET `/teams/:id/roster/:membershipId`
**Response 200** — objeto TeamMember  
**Response 404** `{ "error": "member not found" }`

---

### PATCH `/teams/:id/roster/:membershipId/status`
**Body**
```json
{ "status": "active" }
```

Valores válidos: `active` | `inactive` | `suspended`

**Response 200** `{ "status": "updated" }`

---

## Tipos

### Team
| Campo | Tipo | Notas |
|---|---|---|
| `id` | string (UUID) | |
| `name` | string | |
| `sport_id` | string | ej. `"football"`, `"basketball"` |
| `category` | string | ej. `"Senior"`, `"U18"` |
| `club_id` | string | opcional |
| `logo_url` | string | opcional |
| `fee_amount` | number | en centavos |
| `fee_due_day` | number | 1–31 |
| `currency` | string | ej. `"CLP"`, `"USD"` |
| `is_active` | boolean | |

### TeamMember
| Campo | Tipo | Notas |
|---|---|---|
| `membership_id` | string (UUID) | |
| `user_id` | string (UUID) | |
| `team_id` | string (UUID) | |
| `full_name` | string | |
| `email` | string | |
| `role` | string | `player` \| `coach` \| `admin` |
| `status` | string | `active` \| `inactive` \| `suspended` |
| `jersey_number` | number | opcional |
| `position` | string | opcional |
| `avatar_url` | string | opcional |
| `phone` | string | opcional |
| `joined_at` | string (ISO 8601) | |

---

## Fee Obligations 🔒

### POST `/teams/:id/generate-fees`
Genera cuotas mensuales para todos los miembros activos. Usa `fee_amount`, `fee_due_day` y `currency` del equipo. Es idempotente.

**Body**
```json
{ "period_year": 2026, "period_month": 7 }
```

**Response 201**
```json
{ "created": 5, "skipped": 1, "message": "generated fees for Deportivo Norte 2026/07" }
```

---

### GET `/teams/:id/fees?year=2026&month=7`
Lista cuotas del equipo para un período.

**Response 200**
```json
[
  {
    "id": "uuid",
    "team_id": "uuid",
    "membership_id": "uuid",
    "period_year": 2026,
    "period_month": 7,
    "amount": 15000,
    "currency": "CLP",
    "due_date": "2026-07-10T00:00:00Z",
    "status": "pending",
    "paid_at": null,
    "created_at": "..."
  }
]
```

---

### GET `/memberships/:membershipId/fees`
Lista todas las cuotas de un miembro (ordenadas por período desc).

**Response 200** — array de FeeObligation

---

### GET `/fees/:id`
**Response 200** — objeto FeeObligation

---

### PATCH `/fees/:id/status`
Si `status = "paid"` y no se envía `paid_at`, se usa `NOW()` automáticamente.

**Body**
```json
{ "status": "paid", "paid_at": "2026-07-18T00:00:00Z" }
```

Valores válidos: `pending` | `paid` | `overdue` | `exempted`

**Response 200** — objeto FeeObligation actualizado

---

## Payments 🔒

### POST `/teams/:id/payments`
Registra un pago. Si incluye `obligation_id`, la cuota se marca como `paid` automáticamente.

**Body**
```json
{
  "obligation_id": "uuid",
  "payer_id": "uuid",
  "amount": 10000,
  "currency": "CLP",
  "method": "transfer",
  "notes": "Pago de julio",
  "receipt_url": ""
}
```

`method`: `cash` | `transfer` | `online` | `other`  
`recorded_by` se toma del JWT del usuario autenticado.

**Response 201** — objeto Payment

---

### GET `/teams/:id/payments`
**Response 200** — array de Payment ordenados por fecha desc

---

### GET `/payments/:id`
**Response 200** — objeto Payment

---

### GET `/fees/:id/payment`
Devuelve el pago activo (no revertido) de una cuota.

**Response 200** — objeto Payment  
**Response 404** — sin pago activo

---

### POST `/payments/:id/reverse`
Revierte un pago. Si tenía `obligation_id`, la cuota vuelve a `pending`.

**Response 200** — objeto Payment con `is_reversed: true`  
**Response 409** — ya estaba revertido

---

## Tipos

### FeeObligation
| Campo | Tipo | Notas |
|---|---|---|
| `id` | string (UUID) | |
| `team_id` | string (UUID) | |
| `membership_id` | string (UUID) | |
| `period_year` | number | ej. `2026` |
| `period_month` | number | 1–12 |
| `amount` | number | en centavos |
| `currency` | string | ej. `"CLP"` |
| `due_date` | string (ISO 8601) | |
| `status` | string | `pending` \| `paid` \| `overdue` \| `exempted` |
| `paid_at` | string \| null | se setea automáticamente al pagar |

### Payment
| Campo | Tipo | Notas |
|---|---|---|
| `id` | string (UUID) | |
| `team_id` | string (UUID) | |
| `obligation_id` | string (UUID) | opcional |
| `payer_id` | string (UUID) | quién pagó |
| `recorded_by` | string (UUID) | quién registró (del JWT) |
| `amount` | number | en centavos |
| `currency` | string | |
| `method` | string | `cash` \| `transfer` \| `online` \| `other` |
| `notes` | string | opcional |
| `receipt_url` | string | opcional |
| `is_reversed` | boolean | |
| `created_at` | string (ISO 8601) | |

---

## Competencias 🔒

Una competencia es el paraguas del partido: amistoso, torneo o liga. De ella
cuelgan las entradas de los equipos, las invitaciones, los partidos, los cobros y
los gastos.

Ya existían y no están documentados en detalle acá: `GET|POST
/teams/:id/competitions`, `GET /competitions/:competitionId`, `/entries` e
`/invitations`, `POST /competition-invitations/:invitationId/respond`, y toda la
familia de amistosos (`/teams/:id/friendlies`, `/friendlies/:challengeId` con
`/proposals`, `/counter`, `/accept` y `/decline`).

Toda competencia trae `is_internal`. Es `false` salvo en los partidos internos.

### POST `/teams/:id/internal-matches`
Partido interno: el equipo pone la gente de los dos lados. Requiere rol `manager`.

Es un amistoso sin rival, así que no hay desafío que negociar ni nadie que tenga
que aceptar. Crea la competencia, la entrada del equipo y el partido en una sola
llamada, los tres ya confirmados: la competencia nace `active` —un amistoso normal
nace `draft` porque le falta el sí del rival— y el partido queda con el mismo
equipo de los dos lados, `home_team_id == away_team_id`.

`players_per_side` es **por lado**, igual que en el resto de la API: un fútbol 7
interno se manda con `7`. Quien convoca duplica —son 14 personas— y la cuota sale
de `costo ÷ (players_per_side × 2)`, que es la cuenta de siempre. Lo que cambia es
que esas 14 cuotas salen del propio plantel, así que el equipo se hace cargo del
lugar entero y no de la mitad.

**Body**
```json
{
  "name": "Partido interno",
  "sport_id": "football7",
  "start_at": "2026-08-20T21:00:00Z",
  "venue": "Cancha 3",
  "players_per_side": 7,
  "venue_cost": { "amount": 28000, "currency": "CLP" }
}
```

`name`, `sport_id` y `start_at` son obligatorios; el resto es opcional. Los montos
van en unidades menores de la moneda: CLP no tiene decimales, así que `28000` es
$28.000.

**Response 201**
```json
{
  "competition": {
    "id": "uuid",
    "sport_id": "football7",
    "type": "friendly",
    "name": "Partido interno",
    "organizer_team_id": "uuid",
    "status": "active",
    "start_at": "2026-08-20T21:00:00Z",
    "venue": "Cancha 3",
    "players_per_side": 7,
    "venue_cost": { "amount": 28000, "currency": "CLP" },
    "player_share": 2000,
    "is_internal": true,
    "created_at": "2026-08-12T18:30:00Z"
  },
  "match": {
    "id": "uuid",
    "competition_id": "uuid",
    "home_team_id": "uuid",
    "away_team_id": "uuid",
    "scheduled_at": "2026-08-20T21:00:00Z",
    "venue": "Cancha 3",
    "status": "confirmed",
    "created_at": "2026-08-12T18:30:00Z"
  }
}
```

`player_share` lo calcula el backend al crear y desde ahí se lee: es el número que
el asistente le prometió al manager, y derivarlo en cada lectura dejaría que un
cambio de fórmula alterara en silencio lo que ya se le cobró a la gente.

**Response 400** `{ "error": "match date must be in the future" }`  
**Response 403** — no sos manager del equipo

Desde acá se sigue con lo que ya existe: convocar (`POST /matches/:matchId/callups`)
y repartir el costo (`POST /teams/:id/charges` con
`source: { "type": "match_cost", "id": "<competitionId>" }`).

---

## Charges 🔒

Cobros: plata que un miembro le debe al equipo. Hoy los produce el reparto del
costo de cancha de un partido (`source_type: "match_cost"`, con la competencia
como `source_id`).

Ya existían y no están documentados en detalle acá: `POST /teams/:id/charges`
(reparte), `GET /competitions/:competitionId/charges`, `GET
/memberships/:membershipId/charges`, `POST /charges/:chargeId/receipt`,
`/confirm` y `/reject`, y `GET /teams/:id/funds`.

### GET `/teams/:id/charges?year=2026&month=8`
Los cobros del equipo en un mes. Lo lee cualquier miembro. Sin `year`/`month`
usa el mes en curso.

Un cobro pertenece al mes en que **movió plata**: `confirmed_at` si ya se pagó,
`created_at` si todavía no. Un cobro emitido en julio y pagado en agosto es
recaudación de agosto —es cuando el equipo tuvo el dinero— y contarlo en julio
le pondría ingresos a un mes que ya cerró.

Es la otra mitad del ingreso mensual, junto a `/teams/:id/fees`. Sin esto el
resumen del mes restaba los gastos de un partido sin sumar nunca lo que los
jugadores pagaron por esa misma cancha.

**Response 200** — array de Charge  
**Response 400** `{ "error": "invalid month" }`  
**Response 403** — no sos miembro del equipo

---

## Expenses 🔒

Plata que sale. Los gastos pueden colgar de un partido —el árbitro, las pecheras
de ese amistoso— o ser del equipo a secas (pelotas, botiquín). Los que tienen
`source` entran en el balance de ese partido; el resto solo en el total del mes.

### GET `/teams/:id/expenses?year=2026&month=8`
Gastos del equipo en un mes calendario, por fecha de gasto. Sin `year`/`month`
usa el mes en curso. Lo lee cualquier miembro del equipo.

**Response 200** — array de Expense  
**Response 400** `{ "error": "invalid month" }`  
**Response 403** — no sos miembro del equipo

---

### POST `/teams/:id/expenses`
Anota un gasto. Requiere rol `manager` o `treasurer`.

**Body**
```json
{
  "amount": 20000,
  "currency": "CLP",
  "category": "referee",
  "description": "Árbitro del amistoso",
  "source_type": "match_cost",
  "source_id": "uuid-de-la-competencia",
  "expense_date": "2026-08-09"
}
```

`source_type` y `source_id` son opcionales, pero van **los dos o ninguno**: media
referencia no apunta a nada. La competencia tiene que ser del mismo equipo.
`expense_date` vacío es hoy.

**Response 201** — objeto Expense  
**Response 400** — monto ≤ 0, falta categoría o moneda, origen a medias, fecha mal formada  
**Response 403** — rol insuficiente, o la competencia es de otro equipo  
**Response 404** — la competencia no existe

---

### GET `/competitions/:competitionId/expenses`
Los gastos que cuelgan de ese partido, para cruzarlos con `/competitions/:id/charges`
y armar el balance. Lo lee cualquier miembro del equipo organizador.

**Response 200** — array de Expense  
**Response 404** — la competencia no existe

---

### DELETE `/expenses/:expenseId`
Borra un gasto. No hay edición: un gasto mal cargado se borra y se vuelve a
anotar. Requiere rol `manager` o `treasurer` en el equipo dueño.

**Response 204** — sin cuerpo  
**Response 404** — el gasto no existe

---

### Expense
| Campo | Tipo | Notas |
|---|---|---|
| `id` | string (UUID) | |
| `team_id` | string (UUID) | |
| `recorded_by` | string (UUID) | quién lo anotó (del JWT); vacío si el usuario se borró |
| `amount` | number | en unidades menores de la moneda |
| `currency` | string | ej. `"CLP"` |
| `category` | string | libre; la app usa `referee`, `equipment`, `venue`, `other` |
| `description` | string | opcional |
| `source` | objeto \| ausente | `{ "type": "match_cost", "id": "<competitionId>" }` |
| `expense_date` | string (ISO 8601) | cuándo se gastó, no cuándo se anotó |
| `created_at` | string (ISO 8601) | |

---

## User Profile 🔒

### GET `/users/me`
Retorna el perfil del usuario autenticado.

**Response 200**
```json
{
  "id": "uuid",
  "name": "Mirko Balmaceda",
  "email": "mirko@test.com",
  "tax_id": "123456789",
  "phone": "+56912345678",
  "avatar_url": "https://cdn.example.com/avatar.jpg",
  "favorite_sport": "football",
  "height_cm": 175,
  "weight_kg": 70.5,
  "birth_date": "1998-07-12",
  "alias": "Miri",
  "city": "Santiago",
  "dominant_side": "right",
  "bio": "Mediocampista",
  "created_at": "...",
  "updated_at": "..."
}
```

---

### PATCH `/users/me`
Actualización parcial del perfil. Campos omitidos o vacíos no sobreescriben el valor existente.

`birth_date` va en formato `YYYY-MM-DD`. El RUT (`tax_id`) se normaliza
(sin puntos ni guiones, la K en mayúscula) antes de guardarse; si viene se le
valida el dígito verificador (**400** si no cuadra) y un RUT ya tomado devuelve
**409**.

**Body** _(todos opcionales)_
```json
{
  "name": "Mirko B.",
  "tax_id": "12.345.678-5",
  "phone": "+56912345678",
  "avatar_url": "https://cdn.example.com/avatar.jpg",
  "favorite_sport": "football",
  "height_cm": 175,
  "weight_kg": 70.5,
  "birth_date": "1998-07-12",
  "alias": "Miri",
  "city": "Santiago",
  "dominant_side": "right",
  "bio": "Mediocampista"
}
```

**Response 200** — objeto User actualizado  
**Response 400** `{ "error": "tax_id is not a valid RUT" }`  
**Response 409** `{ "error": "tax id already registered" }`

---

### PUT `/users/me/push-token`
Registra el token de Expo Push Notifications para este dispositivo. Llamar al iniciar la app o cuando el token cambia.

**Body**
```json
{ "token": "ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]" }
```

**Response 200** `{ "status": "ok" }`

---

## Próximos endpoints

- `POST /teams/:id/notifications` — broadcast announcement a todos los miembros
- `GET /users/me/notifications` — notificaciones recibidas
- `PATCH /notifications/:id/read` — marcar como leída
