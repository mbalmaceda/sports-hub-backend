-- Los cargos que esta migración cerró no se pueden devolver a 'submitted': una
-- vez mezclados con los que un tesorero sí confirmó, no hay cómo distinguirlos
-- salvo por `confirmed_by`, que también está vacío en los pagos declarados
-- después del cambio. Revertir por esa señal reabriría cobros legítimos.
--
-- El rollback real es el del código: con el backend viejo, SubmitReceipt vuelve
-- a dejar los cargos nuevos en 'submitted' y el flujo de confirmación funciona
-- otra vez. Lo ya cerrado queda cerrado, que es el resultado correcto.
SELECT 1;
