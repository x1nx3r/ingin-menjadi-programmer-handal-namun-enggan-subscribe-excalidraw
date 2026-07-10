package admin

import (
	"log"
	"net/http"

	"gotth/app/lib"
)

func PageHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := lib.DB.QueryContext(r.Context(), `SELECT email, created_at FROM feature_whitelist ORDER BY created_at ASC`)
	if err != nil {
		log.Printf("list whitelist: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var entries []WhitelistEntry
	for rows.Next() {
		var e WhitelistEntry
		if err := rows.Scan(&e.Email, &e.CreatedAt); err != nil {
			continue
		}
		entries = append(entries, e)
	}

	AdminPage(entries).Render(r.Context(), w)
}

func AddHandler(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	if email == "" {
		http.Error(w, "missing email", http.StatusBadRequest)
		return
	}

	var e WhitelistEntry
	err := lib.DB.QueryRowContext(r.Context(),
		`INSERT OR IGNORE INTO feature_whitelist (email) VALUES (?) RETURNING email, created_at`, email,
	).Scan(&e.Email, &e.CreatedAt)
	if err != nil {
		// Row already existed — INSERT OR IGNORE returns no rows; fetch it.
		err2 := lib.DB.QueryRowContext(r.Context(),
			`SELECT email, created_at FROM feature_whitelist WHERE email = ?`, email,
		).Scan(&e.Email, &e.CreatedAt)
		if err2 != nil {
			log.Printf("add whitelist %s: %v", email, err2)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	WhitelistItem(e).Render(r.Context(), w)
}

func RemoveHandler(w http.ResponseWriter, r *http.Request) {
	email := r.URL.Query().Get("email")
	if email == "" {
		http.Error(w, "missing email", http.StatusBadRequest)
		return
	}

	if _, err := lib.DB.ExecContext(r.Context(), `DELETE FROM feature_whitelist WHERE email = ?`, email); err != nil {
		log.Printf("remove whitelist %s: %v", email, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
