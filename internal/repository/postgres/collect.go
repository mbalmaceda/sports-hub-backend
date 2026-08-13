package postgres

import "github.com/jackc/pgx/v5"

// collect materializa un resultado con el scanner de fila del repositorio.
//
// Reemplaza el `for rows.Next()` a mano que había repetido en cada listado. No
// es más rápido —medido contra el loop manual da lo mismo, porque pgx.CollectRows
// hace exactamente el mismo append— pero deja una sola forma de recorrer filas y
// saca de encima el olvido clásico: chequear rows.Err() al final. pgx lo hace.
//
// El scanner sigue siendo explícito a propósito. pgx trae RowToStructByName, que
// mapea columnas a campos por reflexión y ahorraría también los scanXxx, pero
// cuesta una asignación por fila y midió ~23% más lento. Con scans a mano el
// tipo lo verifica el compilador y no hay etiquetas que mantener sincronizadas
// con el SELECT.
func collect[T any](rows pgx.Rows, scan func(pgx.Row) (T, error)) ([]T, error) {
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (T, error) {
		return scan(row)
	})
}
