package main

// Move only configuration tables out of an older combined cache. Each table
// copy commits into settings before deleting the historical copy, so restart
// can resume without rolling back a saved price. No cross-database write tx.
func (e *Engine) migrateSettings() error {
	for _, table := range []string{"prices", "price_history", "meta"} {
		var old, current bool
		if err := e.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_catalog='history' AND table_schema='main' AND table_name=?)", table).Scan(&old); err != nil {
			return err
		}
		if !old {
			continue
		}
		if err := e.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_catalog='settings' AND table_schema='main' AND table_name=?)", table).Scan(&current); err != nil {
			return err
		}
		if !current {
			tx, err := e.DB.Begin()
			if err != nil {
				return err
			}
			if _, err = tx.Exec("CREATE TABLE settings." + table + " AS SELECT * FROM history." + table); err == nil {
				switch table {
				case "prices":
					_, err = tx.Exec("ALTER TABLE settings.prices ADD PRIMARY KEY(model)")
				case "meta":
					_, err = tx.Exec("ALTER TABLE settings.meta ADD PRIMARY KEY(name)")
				case "price_history":
					_, err = tx.Exec("ALTER TABLE settings.price_history ALTER COLUMN updated_at SET DEFAULT current_timestamp")
				}
			}
			if err != nil {
				tx.Rollback()
				return err
			}
			if err = tx.Commit(); err != nil {
				return err
			}
		}

		if _, err := e.DB.Exec("DROP TABLE history." + table); err != nil {
			return err
		}
	}
	return nil
}
