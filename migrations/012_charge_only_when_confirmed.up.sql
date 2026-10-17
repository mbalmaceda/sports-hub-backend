-- Un cobro de cancha existe si y solo si confirmaste.
--
-- El cobro nace al confirmar la citación (`ensureMatchCharge`) y se borra al
-- bajarse. Lo único que se saltaba esa regla era el viejo "repartir" del
-- manager, que le cobraba también a los citados que nunca contestaron. Ese
-- endpoint ahora solo recuerda; esto limpia lo que dejó.
--
-- Solo pendientes: un cobro pagado o cubierto es plata que existe, aunque la
-- persona ya no figure confirmada, y de ahí sale a quién devolverle.
DELETE FROM charges ch
WHERE ch.source_type = 'match_cost'
  AND ch.status = 'pending'
  AND NOT EXISTS (
      SELECT 1
      FROM match_callups mc
      JOIN matches m ON m.id = mc.match_id
      WHERE m.competition_id = ch.source_id
        AND mc.membership_id = ch.membership_id
        AND mc.status = 'confirmed'
  );

-- Los fondos dejan de guardarse: se derivan de los cobros en
-- `funds.ListByTeam`. La tabla la escribía solo el reparto, así que un partido
-- que nadie repartió no tenía fondo aunque hubiera sobrado plata.
DROP TABLE IF EXISTS team_funds;
