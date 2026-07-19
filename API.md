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

## Próximos endpoints

- `GET /teams/:id/fees` — listar cuotas del equipo
- `POST /teams/:id/fees` — crear cuota
- `GET /users/me/fees` — cuotas del usuario autenticado
- `POST /fees/:id/payments` — registrar pago
- `PUT /users/me/push-token` — registrar token de notificaciones
