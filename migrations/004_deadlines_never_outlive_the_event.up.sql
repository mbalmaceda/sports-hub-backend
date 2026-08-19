-- Ningún plazo de respuesta puede vencer después del evento que se responde.
--
-- Los desafíos nacían con 48 horas fijas y las invitaciones a torneo con siete
-- días, sin mirar cuándo se juega. Un amistoso propuesto para hoy a las 18:00
-- quedaba con vencimiento pasado mañana: el rival veía "1d 20h para responder"
-- sobre un partido que empezaba en cuatro horas, y aceptarlo al día siguiente
-- creaba un partido con fecha pasada.
--
-- El código ya no los emite así (`responseDeadline` en internal/handler), pero
-- las filas que ya estaban en la base siguen con el plazo largo. Esto las baja.
--
-- Solo baja: el LEAST nunca extiende un vencimiento, así que nada que ya esté
-- cerrado o vencido se reabre.

-- Amistosos: manda la última propuesta, que es la fecha que se está negociando.
UPDATE friendly_challenges c
SET expires_at = latest.proposed_start_at
FROM (
    SELECT DISTINCT ON (challenge_id) challenge_id, proposed_start_at
    FROM friendly_proposals
    ORDER BY challenge_id, created_at DESC
) AS latest
WHERE latest.challenge_id = c.id
  AND c.expires_at > latest.proposed_start_at;

-- Torneos y ligas: manda el arranque de la competencia. Sin fecha de arranque
-- no hay techo que aplicar y el plazo original se queda como está.
UPDATE competition_invitations i
SET expires_at = comp.start_at
FROM competitions comp
WHERE comp.id = i.competition_id
  AND comp.start_at IS NOT NULL
  AND i.expires_at > comp.start_at;
