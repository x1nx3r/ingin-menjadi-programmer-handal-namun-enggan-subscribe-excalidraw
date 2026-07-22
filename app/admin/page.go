package admin

import (
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"gotth/app/api"
	"gotth/app/assets"
	"gotth/app/lib"
	"gotth/app/middleware"
)

func getCSSHash() string {
	return assets.CSSHash
}

func PageHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch path {
	case "/admin", "/admin/":
		dashboardHandler(w, r)
	case "/admin/users":
		usersHandler(w, r)
	case "/admin/drawings":
		drawingsHandler(w, r)
	case "/admin/hub":
		hubHandler(w, r)
	case "/admin/system":
		systemHandler(w, r)
	case "/admin/vip":
		vipHandler(w, r)
	case "/admin/users/storage-unlimited-toggle":
		storageUnlimitedToggle(w, r)
	default:
		// Check /admin/users/{uid}
		if len(path) > 13 && path[:13] == "/admin/users/" {
			userDetailHandler(w, r, path[13:])
			return
		}
		http.NotFound(w, r)
	}
}

func dashboardHandler(w http.ResponseWriter, r *http.Request) {
	var totalDrawings, totalUsers, totalWhitelist int

	lib.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM drawings`).Scan(&totalDrawings)
	lib.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&totalUsers)
	lib.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM feature_whitelist`).Scan(&totalWhitelist)

	rooms := api.HubRooms()
	activeRooms := len(rooms)
	wsConns := 0
	for _, room := range rooms {
		wsConns += room.Peers
	}

	dbSize := "N/A"
	if fi, err := os.Stat(lib.GetDBPath()); err == nil {
		dbSize = formatBytes(uint64(fi.Size()))
	}

	rlReports := middleware.RateLimitReports()
	totalBlocked := int64(0)
	for _, r := range rlReports {
		totalBlocked += r.Hits
	}

	stats := DashboardStats{
		TotalDrawings:  totalDrawings,
		TotalUsers:     totalUsers,
		TotalWhitelist: totalWhitelist,
		ActiveRooms:    activeRooms,
		WSConnections:  wsConns,
		DBSize:         dbSize,
		Uptime:         formatUptime(),
		RateLimitHits:  totalBlocked,
	}

	DashboardPage(stats, rlReports).Render(r.Context(), w)
}

func usersHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := lib.DB.QueryContext(r.Context(), `
		SELECT u.uid, u.email, u.name, u.created_at, u.last_seen, COALESCE(u.last_ip, ''),
		       (SELECT COUNT(*) FROM drawings d WHERE d.owner_id = u.uid) AS drawing_count,
		       (SELECT COUNT(*) FROM feature_whitelist fw WHERE fw.email = u.email) > 0 AS is_vip,
		       COALESCE(u.storage_used_bytes, 0), COALESCE(u.storage_max_bytes, 31457280)
		FROM users u
		ORDER BY u.last_seen DESC
	`)
	if err != nil {
		log.Printf("list users: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var users []UserEntry
	for rows.Next() {
		var u UserEntry
		if err := rows.Scan(&u.UID, &u.Email, &u.Name, &u.CreatedAt, &u.LastSeen, &u.LastIP, &u.DrawingCount, &u.IsVIP, &u.StorageUsed, &u.StorageMaxBytes); err != nil {
			log.Printf("scan user: %v", err)
			continue
		}
		users = append(users, u)
	}

	UsersPage(users).Render(r.Context(), w)
}

func userDetailHandler(w http.ResponseWriter, r *http.Request, uid string) {
	var u UserEntry
	err := lib.DB.QueryRowContext(r.Context(), `
		SELECT u.uid, u.email, u.name, u.created_at, u.last_seen, COALESCE(u.last_ip, ''),
		       (SELECT COUNT(*) FROM drawings d WHERE d.owner_id = u.uid),
		       (SELECT COUNT(*) FROM feature_whitelist fw WHERE fw.email = u.email) > 0,
		       COALESCE(u.storage_used_bytes, 0), COALESCE(u.storage_max_bytes, 31457280)
		FROM users u WHERE u.uid = ?
	`, uid).Scan(&u.UID, &u.Email, &u.Name, &u.CreatedAt, &u.LastSeen, &u.LastIP, &u.DrawingCount, &u.IsVIP, &u.StorageUsed, &u.StorageMaxBytes)
	if err != nil {
		log.Printf("user detail %s: %v", uid, err)
		http.NotFound(w, r)
		return
	}

	drows, err := lib.DB.QueryContext(r.Context(), `
		SELECT id, title, created_at, updated_at, share_slug IS NOT NULL AS has_share
		FROM drawings WHERE owner_id = ? ORDER BY updated_at DESC
	`, uid)
	if err != nil {
		log.Printf("user drawings %s: %v", uid, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer drows.Close()

	var drawings []DrawingEntry
	for drows.Next() {
		var d DrawingEntry
		d.OwnerID = uid
		d.OwnerEmail = u.Email
		if err := drows.Scan(&d.ID, &d.Title, &d.CreatedAt, &d.UpdatedAt, &d.HasShare); err != nil {
			continue
		}
		drawings = append(drawings, d)
	}

	UserDetailPage(u, drawings).Render(r.Context(), w)
}

func drawingsHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := lib.DB.QueryContext(r.Context(), `
		SELECT d.id, d.owner_id, COALESCE(u.email, d.owner_id) AS owner_email,
		       d.title, d.created_at, d.updated_at, d.share_slug IS NOT NULL AS has_share
		FROM drawings d
		LEFT JOIN users u ON u.uid = d.owner_id
		ORDER BY d.updated_at DESC
	`)
	if err != nil {
		log.Printf("list drawings: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var drawings []DrawingEntry
	for rows.Next() {
		var d DrawingEntry
		if err := rows.Scan(&d.ID, &d.OwnerID, &d.OwnerEmail, &d.Title, &d.CreatedAt, &d.UpdatedAt, &d.HasShare); err != nil {
			continue
		}
		drawings = append(drawings, d)
	}

	DrawingsPage(drawings).Render(r.Context(), w)
}

func hubHandler(w http.ResponseWriter, r *http.Request) {
	apiRooms := api.HubRooms()
	rooms := make([]HubRoomEntry, len(apiRooms))
	for i, ar := range apiRooms {
		rooms[i] = HubRoomEntry{
			Key:   ar.Key,
			Peers: ar.Peers,
			Msgs:  ar.Msgs,
			Age:   ar.Age,
		}
	}
	HubPage(rooms).Render(r.Context(), w)
}

func systemHandler(w http.ResponseWriter, r *http.Request) {
	memStats := runtime.MemStats{}
	runtime.ReadMemStats(&memStats)

	dbPath := lib.GetDBPath()
	dbSize := formatFileSize(dbPath)
	walSize := formatFileSize(dbPath + "-wal")

	heapMB := formatBytes(memStats.Alloc)

	SystemPage(
		dbSize,
		walSize,
		runtime.Version(),
		runtime.NumCPU(),
		runtime.NumGoroutine(),
		formatUptime(),
		heapMB,
	).Render(r.Context(), w)
}

func formatFileSize(path string) string {
	if fi, err := os.Stat(path); err == nil {
		return formatBytes(uint64(fi.Size()))
	}
	return "N/A"
}

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return strconv.FormatUint(b, 10) + " B"
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatUint(b/div, 10) + string("KMGTPE"[exp]) + "B"
}

func formatUptime() string {
	return time.Since(startTime).Round(time.Second).String()
}

var startTime = time.Now()

func vipHandler(w http.ResponseWriter, r *http.Request) {
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

	VipPage(entries).Render(r.Context(), w)
}

// AddHandler adds an email to the VIP whitelist.
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

	VipItem(e).Render(r.Context(), w)
}

// storageUnlimitedToggle toggles a user's storage between unlimited and default.
func storageUnlimitedToggle(w http.ResponseWriter, r *http.Request) {
	uid := r.FormValue("uid")
	if uid == "" {
		http.Error(w, "missing uid", http.StatusBadRequest)
		return
	}

	var current int64
	err := lib.DB.QueryRowContext(r.Context(),
		`SELECT storage_max_bytes FROM users WHERE uid = ?`, uid,
	).Scan(&current)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	newVal := int64(31457280) // default
	if current != -1 {
		newVal = -1 // toggle to unlimited
	}

	if _, err := lib.DB.ExecContext(r.Context(),
		`UPDATE users SET storage_max_bytes = ? WHERE uid = ?`, newVal, uid,
	); err != nil {
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/users/"+uid, http.StatusSeeOther)
}

// RemoveHandler removes an email from the VIP whitelist.
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
