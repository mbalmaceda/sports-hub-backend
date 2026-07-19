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
Registra un usuario nuevo y devuelve tokens.

**Body**
```json
{
  "name": "Mirko Balmaceda",
  "email": "mirko@example.com",
  "password": "minimo6chars"
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
    "role": "player",
    "created_at": "2026-07-18T00:00:00Z",
    "updated_at": "2026-07-18T00:00:00Z"
  }
}
```

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

## User Profile 🔒

### GET `/users/me`
Retorna el perfil del usuario autenticado.

**Response 200**
```json
{
  "id": "uuid",
  "name": "Mirko Balmaceda",
  "email": "mirko@test.com",
  "phone": "+56912345678",
  "avatar_url": "https://cdn.example.com/avatar.jpg",
  "created_at": "...",
  "updated_at": "..."
}
```

---

### PATCH `/users/me`
Actualización parcial del perfil. Campos omitidos o vacíos no sobreescriben el valor existente.

**Body** _(todos opcionales)_
```json
{
  "name": "Mirko B.",
  "phone": "+56912345678",
  "avatar_url": "https://cdn.example.com/avatar.jpg"
}
```

**Response 200** — objeto User actualizado

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
