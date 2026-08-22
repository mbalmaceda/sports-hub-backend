-- El historial de novedades de una persona, y los dispositivos donde avisarle.
--
-- Hasta acá las notificaciones solo existían en el aire: tres `NotifyAsync`
-- mandaban un push a Expo y ahí terminaba todo. Si el teléfono estaba apagado,
-- si la persona limpió la bandeja, o si simplemente la tocó y la app la abrió en
-- el inicio, no quedaba rastro de que la habían convocado, cobrado o retado. La
-- pantalla de novedades mostraba lo único que el backend sabía listar
-- —invitaciones a equipo pendientes— y por eso parecía abandonada: estaba vacía
-- porque no había dónde guardar.
--
-- Esta migración trae las dos mitades que faltaban.

-- ─── Notificaciones ─────────────────────────────────────────────────────────
--
-- Una fila por destinatario y por evento. Es el registro, y el push sale de
-- ella: emitirlos por vías separadas garantiza que un día la lista diga una
-- cosa y la bandeja del teléfono otra.
--
-- La fila es histórica y lo que apunta está vivo. "Te retaron el 12 de agosto"
-- es cierto para siempre; el desafío puede estar vencido, aceptado o cancelado
-- cuando la tocan. Por eso acá no hay estado del evento ni acciones: la fila
-- dice qué pasó y a dónde ir, y la pantalla de destino —que ya sabe de plazos y
-- de permisos— decide qué se puede hacer al llegar.
CREATE TABLE notifications (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),

    -- A quién. Es el usuario y no la membresía: el token de push está en la
    -- cuenta, y hay notificaciones que llegan antes de pertenecer a ningún
    -- equipo (la invitación a sumarse es exactamente ese caso).
    recipient_user_id UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- De qué equipo es el asunto. Siempre hay uno, incluso cuando el
    -- destinatario todavía no es miembro: en la invitación a un equipo es el
    -- equipo que invita.
    team_id           UUID        NOT NULL REFERENCES teams(id) ON DELETE CASCADE,

    -- Qué pasó. El tipo es lo que la app traduce a una ruta, en un solo lugar
    -- (`notificationRoute`), así que agregar uno acá obliga a decidir a dónde
    -- lleva. Sin CHECK a propósito: la lista crece seguido y un tipo nuevo no
    -- puede pedir migración.
    type              TEXT        NOT NULL,

    -- A qué apunta. El `type` ya dice de qué entidad se trata —un desafío, un
    -- partido, un cobro— así que no hace falta guardarlo aparte. Nulo es una
    -- notificación que solo informa y no navega a ningún lado (un anuncio del
    -- equipo), y el modelo tiene que admitirlo: si toda fila tuviera que
    -- enrutar, terminaríamos inventando destinos para que no queden muertas.
    entity_id         UUID,

    -- Texto ya resuelto. Se guarda armado y no como plantilla + parámetros
    -- porque una notificación es lo que se dijo entonces: si el equipo se
    -- renombra, el aviso de hace un mes tiene que seguir diciendo el nombre que
    -- tenía cuando pasó.
    title             TEXT        NOT NULL,
    body              TEXT        NOT NULL,

    -- Cuándo se leyó, no si se leyó. Un booleano contesta menos por el mismo
    -- espacio, y el "no leídas" sale igual con `read_at IS NULL`.
    read_at           TIMESTAMPTZ,

    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Cuándo pasó lo que la fila reporta, que es lo que se muestra y por lo que
    -- se ordena. Igual a created_at salvo en las que se colapsan (ver abajo):
    -- ahí la fila es la misma y lo que se mueve es esta fecha.
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- El listado de una persona, que es la única consulta que existe.
CREATE INDEX notifications_recipient_idx
    ON notifications (recipient_user_id, updated_at DESC);

-- El punto de la campana. Parcial porque las leídas no se cuentan nunca, y en
-- una cuenta vieja son casi todas.
CREATE INDEX notifications_unread_idx
    ON notifications (recipient_user_id)
    WHERE read_at IS NULL;

/*
Las que se colapsan.

Un fútbol 7 interno junta catorce personas, y una fila por cada respuesta a la
citación sepulta el resto de la lista para el manager. `callup_responses` es una
sola fila por (manager, partido) que se reescribe: "12 de 14 confirmaron". La
fila se mantiene y lo que se mueve es `updated_at`, así que vuelve arriba cada
vez que alguien contesta.

El índice es parcial y nombra los tipos uno por uno a propósito. Colapsar es la
excepción —de dieciséis flujos, uno— y que agregar otro obligue a tocar el
esquema es correcto: es una decisión de diseño, no un detalle de implementación.
*/
CREATE UNIQUE INDEX notifications_collapsed_key
    ON notifications (recipient_user_id, type, entity_id)
    WHERE type IN ('callup_responses');

-- ─── Tokens de push, uno por dispositivo ────────────────────────────────────
--
-- `users.push_token` era una columna sola: entrar en un segundo teléfono
-- pisaba el token del primero y lo dejaba mudo sin avisar. Con tres avisos era
-- un detalle; con dieciséis es media nómina que no recibe nada.
--
-- La clave primaria es el token y no un id propio, porque el token ES el
-- dispositivo. Cuando alguien cierra sesión y entra otra persona en el mismo
-- teléfono, Expo devuelve el mismo token: el `ON CONFLICT` lo reasigna al
-- usuario nuevo, que es exactamente lo que corresponde —el aparato es de quien
-- lo tiene en la mano ahora—.
CREATE TABLE push_tokens (
    token        TEXT        PRIMARY KEY,
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- La app registra en cada arranque. Sirve para poder barrer los muertos el
    -- día que haga falta: un token que Expo ya no acepta no se entera solo.
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX push_tokens_user_idx ON push_tokens (user_id);

-- Lo que ya estaba registrado se muda. Sin esto, todo el mundo queda sin push
-- hasta que vuelva a abrir la app.
INSERT INTO push_tokens (token, user_id)
SELECT push_token, id
FROM users
WHERE push_token IS NOT NULL AND push_token <> ''
ON CONFLICT (token) DO NOTHING;

-- Y la columna se va: dos lugares donde vive el mismo dato son dos lugares que
-- pueden discrepar, y el que quede primero en un `SELECT` decide quién recibe.
ALTER TABLE users DROP COLUMN push_token;
