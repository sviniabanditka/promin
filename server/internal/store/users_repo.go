package store

import (
	"database/sql"
	"errors"
	sqlite "modernc.org/sqlite"
)

// ErrLoginTaken is returned by UsersRepo.Create when the login already
// exists (UNIQUE constraint on users.login).
var ErrLoginTaken = errors.New("store: login already taken")

// ErrPinTaken is returned when a PIN's lookup hash collides with another
// profile (partial-unique index idx_users_pin). A 6-digit PIN maps to exactly
// one profile, so duplicates are rejected at set time.
var ErrPinTaken = errors.New("store: pin already taken")

// User is a row of the users table (see docs/data-model.md).
// There is no separate "role" column: per docs/backend.md,
// "admin" is a convention, not data — the first registered user (id == 1).
type User struct {
	ID        int64
	Login     string
	PassHash  string
	CreatedAt int64
	// PinLookup = hex(HMAC-SHA256(pinSecret, pin)); NULL = profile has no PIN
	// (not enterable — the safe closed-by-default state). Never surfaced to
	// clients; only used to resolve a PIN to a profile in O(1).
	PinLookup sql.NullString
}

// IsAdmin reports whether u is the first registered user, per the
// convention documented on User.
func (u User) IsAdmin() bool { return u.ID == 1 }

// UsersRepo is the repository over the users table.
type UsersRepo struct {
	db *sql.DB
}

// Create inserts a new user. Returns ErrLoginTaken if login is already in
// use.
func (r *UsersRepo) Create(login, passHash string, createdAt int64) (User, error) {
	res, err := r.db.Exec(
		`INSERT INTO users (login, pass_hash, created_at) VALUES (?, ?, ?)`,
		login, passHash, createdAt,
	)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return User{}, ErrLoginTaken
		}
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	return User{ID: id, Login: login, PassHash: passHash, CreatedAt: createdAt}, nil
}

const userCols = `id, login, pass_hash, created_at, pin_lookup`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Login, &u.PassHash, &u.CreatedAt, &u.PinLookup)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// GetByLogin returns the user with the given login, or ErrNotFound.
func (r *UsersRepo) GetByLogin(login string) (User, error) {
	return scanUser(r.db.QueryRow(`SELECT `+userCols+` FROM users WHERE login = ?`, login))
}

// GetByID returns the user with the given id, or ErrNotFound.
func (r *UsersRepo) GetByID(id int64) (User, error) {
	return scanUser(r.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// GetByPinLookup resolves a PIN (via its HMAC lookup hash) to a profile in O(1),
// or ErrNotFound. The partial-unique index guarantees at most one match.
func (r *UsersRepo) GetByPinLookup(lookup string) (User, error) {
	return scanUser(r.db.QueryRow(`SELECT `+userCols+` FROM users WHERE pin_lookup = ?`, lookup))
}

// List returns all users (admin roster), oldest first.
func (r *UsersRepo) List() ([]User, error) {
	rows, err := r.db.Query(`SELECT ` + userCols + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetPIN sets (or, with a NULL lookup, clears) a profile's PIN lookup hash.
// Returns ErrPinTaken if another profile already owns that PIN.
func (r *UsersRepo) SetPIN(id int64, lookup sql.NullString) error {
	_, err := r.db.Exec(`UPDATE users SET pin_lookup = ? WHERE id = ?`, lookup, id)
	if isUniqueConstraintErr(err) {
		return ErrPinTaken
	}
	return err
}

// UpdateLogin renames a profile. Returns ErrLoginTaken on collision.
func (r *UsersRepo) UpdateLogin(id int64, login string) error {
	_, err := r.db.Exec(`UPDATE users SET login = ? WHERE id = ?`, login, id)
	if isUniqueConstraintErr(err) {
		return ErrLoginTaken
	}
	return err
}

// UpdatePassHash resets a user's argon2 password hash (admin password reset).
func (r *UsersRepo) UpdatePassHash(id int64, hash string) error {
	_, err := r.db.Exec(`UPDATE users SET pass_hash = ? WHERE id = ?`, hash, id)
	return err
}

// Delete removes a profile (its per-user data cascades). The admin (id==1)
// cannot be deleted.
func (r *UsersRepo) Delete(id int64) error {
	if id == 1 {
		return errors.New("store: cannot delete admin (user 1)")
	}
	_, err := r.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// Count returns the total number of registered users. Used to decide
// whether a registration is the first (auto-admin) one.
func (r *UsersRepo) Count() (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// isUniqueConstraintErr reports whether err is a SQLite UNIQUE / PRIMARY KEY
// constraint violation, by the driver's typed error code rather than by
// substring-matching its message (which a driver upgrade could reword).
func isUniqueConstraintErr(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	const sqliteConstraintUnique, sqliteConstraintPrimaryKey = 2067, 1555
	return se.Code() == sqliteConstraintUnique || se.Code() == sqliteConstraintPrimaryKey
}
