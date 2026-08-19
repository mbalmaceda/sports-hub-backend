# Sports Hub API

**Base URL producción:** `https://sports-hub-backend.fly.dev`  
**Base URL local:** `http://localhost:8085`

Todos los endpoints protegidos requieren header:
```
Authorization: Bearer <access_token>
```

El access token dura **15 minutos**. Cuando vence, la respuesta es **401** y el
cliente tiene que llamar a `/auth/refresh` y reintentar. Es el flujo normal, no
un error: la sesión larga la sostiene el refresh token, que dura 30 días.

Toda respuesta trae `X-Request-Id`. Si algo falla, ese id es lo que permite
encontrar la request en los logs.

### Límites de peticiones

| Endpoint | Límite |
|---|---|
| `POST /auth/login` | 10/min por IP, y 5 cada 15 min por cuenta |
| `POST /auth/register` | 5/hora por IP |
| `POST /auth/refresh` | 30/min por IP |
| `GET /people/lookup`, `GET /teams/search` | 30/min por usuario |

Al pasarse, la respuesta es **429** con header `Retry-After` en segundos.

El cuerpo de un request no puede pasar de **1 MB**; más que eso devuelve **413**.

---

## Auth

### POST `/auth/register`
Registra un usuario nuevo y devuelve tokens. Los campos del perfil deportivo
son opcionales: la cuenta se crea igual sin ellos.

`birth_date` va en formato `YYYY-MM-DD`. El RUT (`tax_id`) se normaliza
(sin puntos ni guiones, la K en mayúscula) antes de guardarse; intentar
registrar un RUT ya existente devuelve **409**.

`tax_id` es opcional, pero si viene se valida el dígito verificador con el
módulo 11 chileno: uno inventado devuelve **400**. La app móvil ya no lo manda
—dejó de pedirlo en el alta— así que las cuentas nuevas nacen sin RUT y la
búsqueda del manager (`GET /people/lookup`) las encuentra por `email`, no por
`tax_id`.

La contraseña solo tiene tope: **hasta 72 bytes**, que es el límite de bcrypt,
que ignora en silencio lo que pase de ahí. No hay largo mínimo ni reglas de
composición; vacía devuelve **400** porque el campo es obligatorio. El email se
guarda en minúscula.

**Body**
```json
{
  "name": "Mirko Balmaceda",
  "email": "mirko@example.com",
  "password": "la-que-quiera",
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
El email no distingue mayúsculas: `Mirko@x.com` y `mirko@x.com` son la misma
cuenta.

**Body**
```json
{ "email": "mirko@example.com", "password": "la-que-quiera" }
```

**Response 200** — igual que register  
**Response 401** `{ "error": "invalid credentials" }` — mismo mensaje si el email
no existe o si la contraseña está mal  
**Response 429** — demasiados intentos, ver `Retry-After`

---

### POST `/auth/refresh`
El refresh token se **rota** en cada uso: el que se manda deja de servir y la
respuesta trae uno nuevo. Hay que guardar el nuevo.

**Reutilización.** Mandar un refresh token ya rotado revoca **todas** las
sesiones de esa cadena, incluida la de quien lo esté usando. Es la defensa
contra un token robado, y significa que el cliente no puede reintentar con un
token viejo: si perdió el nuevo, tiene que volver a hacer login.

Dos refresh simultáneos con el mismo token —lo que pasa cuando varias requests
reciben 401 a la vez— **no** cuentan como reutilización: hay una ventana de 30
segundos en la que los dos reciben un token válido.

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

**Response 401** `{ "error": "invalid or expired refresh token" }` — token
inexistente, vencido, revocado o reutilizado; no se distingue cuál

---

### POST `/auth/logout`
Cierra la sesión de este dispositivo: revoca la cadena entera, no solo el último
token.

Devuelve **200** aunque el token no exista, para no confirmar cuáles son válidos.

**Body**
```json
{ "refresh_token": "5e7a..." }
```

**Response 200** `{ "status": "ok" }`

---

### POST `/auth/logout-all` 🔒
Cierra **todas** las sesiones del usuario, en todos sus dispositivos. Requiere
access token válido.

Sirve para el caso "creo que alguien entró a mi cuenta".

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
    "city": "Santiago",
    "description": "Entrenamos los jueves en Ñuñoa.",
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
  "city": "Santiago",
  "description": "Entrenamos los jueves en Ñuñoa.",
  "club_id": "",
  "logo_url": "",
  "fee_amount": 10000,
  "fee_due_day": 5,
  "currency": "CLP"
}
```

`name`, `sport_id`, `category` y `currency` son obligatorios; `city` y `description`
son opcionales y viajan vacías si el alta no las pidió.

**Response 201** — objeto Team  
**Response 409** `{ "error": "team name already taken" }` — el nombre ya existe (sin distinguir mayúsculas)

---

### GET `/teams/:id`
**Response 200** — objeto Team  
**Response 404** `{ "error": "team not found" }`

---

### PATCH `/teams/:id/fee-config`
`fee_amount` en **0 apaga la cuota mensual**, y es un valor válido: el equipo que no cobra
cuota deja de ver la pestaña de Cuotas en el móvil. Por eso el monto viaja como puntero del
lado del servidor — con un `int64` a secas, el `required` de gin rechazaba el cero y la
cuota no se podía desactivar nunca.
**Body**
```json
{ "fee_amount": 15000, "fee_due_day": 10 }
```

**Response 200** — objeto Team actualizado

---

## Roster 🔒

### GET `/teams/:id/roster`
Lista todos los miembros del equipo. Combina datos de `memberships` + `users`.

Hay que **pertenecer al equipo** para leerlo, con cualquier rol: tener sesión no
alcanza. Una membresía dada de baja tampoco.

El listado **no trae `email` ni `phone`**. El contacto se pide de a uno, en
`GET /memberships/:membershipId`: mandarlo acá entregaba la agenda completa del
club en un solo request, y ninguna pantalla lo usa desde la lista.

**Response 200**
```json
[
  {
    "membership_id": "uuid",
    "user_id": "uuid",
    "team_id": "uuid",
    "full_name": "Mirko Balmaceda",
    "avatar_url": "",
    "role": "player",
    "plays_as_player": true,
    "jersey_number": 10,
    "position": "Delantero",
    "status": "active",
    "joined_at": "2026-07-18T00:00:00Z"
  }
]
```

**Response 403** `{ "error": "you are not a member of this team" }`

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
Alias de `GET /memberships/:membershipId`.

Ficha completa del jugador, y la **única** que devuelve `email` y `phone`. Exige
pertenecer al equipo de esa membresía, con cualquier rol.

**Response 200** — objeto TeamMember  
**Response 403** `{ "error": "you are not a member of this team" }`  
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
| `city` | string | texto libre, puede venir vacío |
| `description` | string | presentación breve, hasta 160 caracteres; puede venir vacía |
| `club_id` | string | opcional |
| `logo_url` | string | opcional |
| `fee_amount` | number | entero en **unidades menores** según el exponente ISO 4217 — CLP tiene exponente 0, así que `28000` es $28.000 |
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
| `email` | string | **solo en la ficha individual**, no en el listado del plantel |
| `role` | string | `player` \| `treasurer` \| `manager` |
| `kind` | string | `member` \| `guest`; el invitado de un partido no es del club |
| `plays_as_player` | boolean | si ocupa un lugar en el plantel; independiente del rol |
| `status` | string | `active` \| `inactive` \| `suspended` |
| `jersey_number` | number | opcional |
| `position` | string | opcional |
| `avatar_url` | string | opcional |
| `phone` | string | opcional, **solo en la ficha individual** |
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

### Quién puede leer qué

Tener sesión **no alcanza** para nada de esto. La regla es "quién lo juega":

| Endpoint | Lo lee |
|---|---|
| `GET /competitions/:competitionId` · `/entries` · `/matches` | el plantel de algún equipo que la juega (organizador o con entrada), o el invitado con convocatoria a uno de sus partidos |
| `GET /matches/:matchId` · `/callups` | el plantel de cualquiera de los dos equipos, o el invitado citado a **ese** partido |

Los tres primeros no validaban nada hasta la 002: alcanzaba con el UUID para leer
la competencia de cualquier club, con su fecha, su cancha y cuánto cuesta.

El invitado entra por su convocatoria y no por su membresía, y eso es lo que lo
acota: necesita leer la competencia porque de ahí sale su cuota por jugador, pero
una membresía de invitado por sí sola no abre ninguna otra. La regla vive en un
solo lugar, `internal/handler/competition_access.go`.

**Response 403** `{ "error": "you are not a member of this team" }` |
`{ "error": "your invitation only covers the match you were called up to" }`

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

### PUT `/matches/:matchId/result`
El marcador, que es lo que cierra el partido. Requiere rol `manager` en alguno de
los dos equipos: los dos lo vieron, y esperar a que lo cargue uno solo deja la
mitad de los encuentros sin resultado.

```json
{ "team_id": "uuid", "home_score": 3, "away_score": 2 }
```

Es un **upsert**: mandarlo de nuevo corrige el resultado en vez de agregar otro.
Un marcador se anota de memoria un rato después del partido y equivocarse en un
gol es normal.

Guardarlo deja el partido en `completed`, en la misma sentencia. Y si a la
competencia no le queda ningún partido sin resultado, pasa a `finished` — en un
amistoso es inmediato, porque tiene uno solo.

**Response 200** — el partido, ya con `home_score`, `away_score`,
`result_recorded_at` y `result_recorded_by`.

Todo partido trae esos cuatro campos, ausentes mientras nadie cargó el resultado.
Ausente **no es** `0`: un empate sin goles es un resultado, y por eso los goles
viajan como punteros y el 0 a 0 se acepta.

**Response 400** `{ "error": "scores must be between 0 and 999" }` |
`{ "error": "that team does not play this match" }`  
**Response 409** `{ "error": "match has not been played yet" }` — antes de la hora
de inicio no hay resultado que cargar. Es el mismo techo que ya usan los plazos
de respuesta y los enlaces de invitado: nada de lo de antes del partido sobrevive
al partido, y el marcador empieza justo ahí.  
**Response 403** — no sos manager de ninguno de los dos equipos

---

## Liquidaciones entre equipos 🔒

La mitad de la cancha que un equipo le transfiere al otro.

El costo del lugar se reparte entre los que entran por los dos lados y cada
equipo le cobra a los suyos —eso ya funcionaba— pero la cancha la reserva y la
paga **uno solo**: el organizador. Sin este tramo, el rival cobraba sus $14.000
y esa plata se quedaba en su cuenta, mientras el organizador terminaba cada
amistoso $14.000 abajo.

Es **una** transferencia entre managers, no catorce entre desconocidos.

- **Deudor**: el equipo retado (`challenged_team_id`).
- **Acreedor**: el que desafió, que es el organizador de la competencia.
- **Monto**: la mitad del costo del lugar, redondeada hacia arriba — el mismo
  `TeamShare` que usa el reparto, para que no quede un peso colgado.

Nace al aceptarse el amistoso (`POST /friendlies/:challengeId/accept`), que es
cuando el compromiso existe. Cancha gratis o partido interno no generan ninguna.

```json
{
  "id": "uuid",
  "source": { "type": "match_cost", "id": "<competitionId>" },
  "from_team_id": "uuid",
  "to_team_id": "uuid",
  "amount": 14000,
  "currency": "CLP",
  "status": "pending",
  "paid_at": null,
  "created_at": "2026-08-12T18:30:00Z"
}
```

### GET `/teams/:id/settlements`
Las dos direcciones: lo que el equipo debe y lo que le deben. Requiere rol
`manager` o `treasurer` — es plata del equipo, no la lee todo el plantel.

### GET `/competitions/:competitionId/settlement`
La deuda de un partido, para el balance. Mismo alcance que la competencia: la lee
quien la juega.

**Response 404** `{ "error": "this competition has no settlement between teams" }`
— caso normal, no un error: cancha gratis o partido interno.

### GET `/settlements/:settlementId/bank-account`
A qué cuenta transferir: la del equipo que **cobra**. Requiere `manager` o
`treasurer` del equipo que **debe**.

Existe porque `GET /teams/:id/bank-account` exige ser de ese equipo y el rival
justamente no lo es. Va por su propio endpoint y no adentro de la liquidación
porque la liquidación se lista en el inicio, y datos bancarios en una respuesta
de lista viajan cada vez que alguien abre la app.

**Response 404** — el organizador todavía no cargó sus datos bancarios.

### POST `/settlements/:settlementId/pay`
El deudor declara la transferencia. Sin body. Requiere `manager` o `treasurer`
del equipo que debe: **el acreedor no puede** cerrarla, y esa guarda es la que
sostiene el modelo — acá nadie verifica nada, se le cree al que dice haber
transferido.

No pide comprobante a propósito: la app no tiene dónde guardar la imagen (ver la
deuda técnica del comprobante en el CLAUDE.md del móvil), y pedir uno que se
descarta sería repetir a sabiendas algo que ya no funciona.

**Response 409** `{ "error": "this settlement was already paid" }`  
**Response 403** — no manejás la plata del equipo que debe

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

### POST `/charges/:chargeId/waive`
Condona el cobro: lo cierra sin que entre plata por la app. Es el "me lo pagó en
efectivo" y el "déjalo así, no lo vamos a ver".

Lo hace **manager o tesorero**, nunca el deudor —si no, cualquiera se perdona su
propia cuota— y solo sobre un cobro `pending`. Uno ya pagado no se condona: esa
plata entró, y borrarla del registro es peor que la deuda.

Es la única salida que tiene un pendiente incobrable. Con los invitados de un
partido deja de ser un caso raro: al que vino un sábado y no volvió no hay cuota
mensual donde arrastrarle la deuda.

**Response 200** — objeto Charge en `waived`  
**Response 403** `{ "error": "your role does not allow this action" }`  
**Response 409** `{ "error": "this charge was already paid" }`

---

## Invitados de un partido ("parches")

Gente que no es del equipo y viene a completar una convocatoria. El manager
genera un enlace, lo comparte por WhatsApp, y quien lo abre crea su cuenta (el
`POST /auth/register` de siempre) y canjea.

El invitado queda como una **membresía con `kind: "guest"`**: la convocatoria, el
cobro de la cancha y el historial cuelgan de `membership_id`, así que modelarlo
aparte obligaba a duplicar el flujo de cobros entero. Lo que lo distingue del
plantel:

| | Plantel | Invitado |
|---|---|---|
| Cuota mensual del club | sí | **no** |
| Cuota de cancha del partido | sí | **sí**, la misma |
| Ve el plantel / las finanzas | sí | **no** |
| Ve partidos del equipo | todos | **solo al que fue convocado** |

Ese último corte es lo que hace `requireMember` (excluye invitados) contra
`requireMembership` (los incluye, y quien la usa los acota al partido). En
Firestore lo repiten las reglas con `kind` y `matchId` del espejo.

---

### POST `/matches/:matchId/guest-invites` 🔒
Genera el enlace. Solo el **manager** de uno de los equipos que juega: sumar
gente al partido es convocar.

`max_uses` lo manda el cliente porque es él quien sabe cuántos faltan, pero el
servidor lo acota a **11**. El vencimiento no se negocia: es la hora del partido.
Las dos cosas juntas son lo que evita el enlace huérfano circulando en un grupo
de WhatsApp tres meses después.

**Body**
```json
{ "team_id": "uuid", "max_uses": 3 }
```

**Response 201**
```json
{
  "id": "uuid",
  "token": "aX9...",
  "url": "https://sports-hub-backend.fly.dev/i/aX9...",
  "match_id": "uuid",
  "team_id": "uuid",
  "max_uses": 3,
  "used_count": 0,
  "expires_at": "2026-08-16T23:00:00Z"
}
```

⚠️ El `token` se ve **una sola vez**, acá. En la base solo vive su SHA-256. Si se
pierde, se genera otro enlace.

`url` es el enlace listo para compartir y lo arma el servidor, no el cliente: es
el único que sabe con qué dominio se sirven las invitaciones (`PUBLIC_BASE_URL`).
Llega **vacío** si esa variable no está configurada, y en ese caso el cliente no
tiene que compartir nada — mandar un enlace roto por WhatsApp se paga con la
persona que lo recibió.

**Response 400** `{ "error": "max_uses is too high" }`  
**Response 403** — no sos manager de ese equipo  
**Response 409** `{ "error": "this match already started" }`

---

### GET `/invites/:token` — **público, sin sesión**
La pantalla que ve quien recibe el enlace y todavía no tiene cuenta. Por eso no
pide token de sesión.

Devuelve lo mínimo: equipo, cuándo, dónde, quién invita, cuánto sale y cuántos
lugares quedan. **Nada de plantel, finanzas ni datos de contacto de nadie**: es
una URL que cualquiera con el token puede abrir, así que lo que sale por acá se
considera publicado.

El costo va sí o sí. El parche que se entera en la cancha de que debe la cuota
es una pelea, y el que queda mal es el que lo invitó.

Limitado a 60 req/min por IP: es el único GET anónimo de la app.

**Response 200**
```json
{
  "team_name": "Deportivo Norte",
  "invited_by": "Mirko",
  "scheduled_at": "2026-08-16T23:00:00Z",
  "venue": "Cancha La Reina",
  "is_internal": false,
  "opponent_name": "Stars FC",
  "cost_per_player": 2000,
  "currency": "CLP",
  "remaining_uses": 2,
  "expires_at": "2026-08-16T23:00:00Z"
}
```

**Response 404** — el token no existe  
**Response 410** `{ "error": "this invitation link is no longer valid" }` — vencido,
revocado o sin cupo. Los tres devuelven lo mismo a propósito: al que llegó tarde
hay que decirle que llegó tarde, y a quien esté probando tokens no hay que
contarle más.

---

### POST `/invites/:token/accept` 🔒
Canjea el enlace con la cuenta de quien lo abre. Registrarse es un paso aparte
(`POST /auth/register`): un segundo camino de alta de usuarios traería su propia
validación, su propio rate limit y su propia forma de romperse.

En una transacción: descuenta el cupo, crea la membresía `guest` y deja la
convocatoria en `confirmed` —el que canjea ya dijo que va—. Después emite el
cargo de la cancha, la misma cuota que paga el resto.

**Response 200**
```json
{ "membership_id": "uuid", "team_id": "uuid", "match_id": "uuid" }
```

**Response 409** `{ "error": "you already belong to this team" }` — el del plantel
que abre el enlace del grupo. No gasta un lugar de nadie.  
**Response 410** — el enlace ya no sirve

---

### GET `/matches/:matchId/guest-invites` 🔒
Los enlaces de un partido, para que el manager vea cuántos lugares repartió
antes de generar otro. Sin el token: ese ya no existe en claro.

**Response 200** — array de `{ id, team_id, max_uses, used_count, remaining_uses, usable, expires_at, created_at }`

---

### DELETE `/guest-invites/:inviteId` 🔒
Apaga el enlace antes de tiempo —se mandó al grupo equivocado—. No saca a nadie
que ya haya entrado: esa gente está convocada y probablemente ya pagó.

**Response 200** `{ "status": "revoked" }`

---

### POST `/teams/:id/roster/:membershipId/promote` 🔒
Suma al plantel a un invitado que ya jugó: deja de ser `kind: "guest"`.

Lo hace el **manager**, y es un acto explícito y no algo que pase solo después de
N partidos, porque cambia dos cosas de fondo para esa persona: empieza a ver el
equipo entero y empieza a deberle la cuota mensual.

**Response 200** — objeto TeamMember, ya con `kind: "member"`  
**Response 403** — no sos manager de ese equipo  
**Response 404** — la membresía no existe o es de otro equipo  
**Response 409** `{ "error": "this member already belongs to the squad" }`

---

## Páginas públicas de enlaces — **sin sesión**

Las sirve el propio backend porque el destinatario de un enlace de invitación es,
por definición, alguien que todavía no tiene la app: un `zports://` en WhatsApp
no le abre nada y ni siquiera es tocable, así que el enlace tiene que ser
`https://` y algo tiene que responderlo. Ver `internal/handler/applinks_handler.go`
y la sección "Enlaces de invitación" de `DEPLOY.md`.

### GET `/i/:token`
HTML, no JSON. La página que abre quien recibe el enlace: dice a qué lo invitan y
ofrece descargar la app. Sin JavaScript, porque se abre dentro del navegador de
WhatsApp con la red que haya.

Con la app instalada y los App Links verificados, el sistema operativo intercepta
antes y abre la app directo — esta página es lo que ve quien **no** la tiene.

**200** invitación vigente · **404** no existe · **410** vencida, revocada o llena

### GET `/.well-known/assetlinks.json`
Lo que hace que Android abra la app en vez del navegador. Sale de
`ANDROID_CERT_FINGERPRINTS`.

**404** si no está configurado, a propósito: un array vacío le diría a Android
"acá no hay ninguna app asociada" y se queda cacheado así.

### GET `/.well-known/apple-app-site-association`
El equivalente de iOS, desde `APPLE_APP_ID` (`<TeamID>.<BundleID>`). **404**
mientras no exista la cuenta de Apple Developer.

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
