package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

var (
	ErrNotFound error = errors.New("not found")
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id   INTEGER PRIMARY KEY,
	name TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS passwords (
	owner_id      INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	password_hash BLOB NOT NULL,
	salt          BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS tasks (
	id       INTEGER PRIMARY KEY,
	owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	body     TEXT NOT NULL,
	state    INTEGER NOT NULL DEFAULT 0
);`

const q_listUsers = "SELECT id,name FROM users;"
const q_addUser = "INSERT INTO users (name) VALUES (?) RETURNING id,name;"
const q_getUser = "SELECT id,name FROM users WHERE id = ?;"
const q_deleteUser = "DELETE FROM users WHERE id = ?;"
const q_updateUserName = `UPDATE users SET name = ? WHERE id = ?
						  RETURNING id,name;`
const q_addPassword = `INSERT INTO passwords (owner_id,password_hash,salt)
					   VALUES (?,?,?);`
const q_deletePassword = "DELETE FROM passwords WHERE owner_id = ?;"
const q_addTask = `INSERT INTO tasks (owner_id,body) VALUES (?,?)
				   RETURNING id,owner_id,body,state;`
const q_deleteTask = "DELETE FROM tasks WHERE id = ? AND owner_id = ?;"
const q_switchTask = `UPDATE tasks SET state = ? WHERE id = ?
					  AND owner_id = ? RETURNING id,owner_id,body,state;`
const q_changeTask = `UPDATE tasks SET body = ? WHERE id = ?
					  AND owner_id = ? RETURNING id,owner_id,body,state;`
const q_listTasks = "SELECT id,owner_id,body,state FROM tasks;"
const q_getTask = `SELECT id,owner_id,body,state FROM tasks
				   WHERE id = ? AND owner_id = ?;`
const q_listUserTasks = `SELECT id,owner_id,body,state FROM tasks
						 WHERE owner_id = ?;`
const q_deleteAllUserTasks = "DELETE FROM tasks WHERE owner_id = ?;"

type Store struct {
	db *sql.DB

	st_addUser        *sql.Stmt
	st_listUsers      *sql.Stmt
	st_getUser        *sql.Stmt
	st_deleteUser     *sql.Stmt
	st_updateUserName *sql.Stmt

	st_addPassword    *sql.Stmt
	st_deletePassword *sql.Stmt

	st_addTask            *sql.Stmt
	st_deleteTask         *sql.Stmt
	st_getTask            *sql.Stmt
	st_switchTask         *sql.Stmt
	st_changeTask         *sql.Stmt
	st_listTasks          *sql.Stmt
	st_listUserTasks      *sql.Stmt
	st_deleteAllUserTasks *sql.Stmt
}

func (s *Store) Close() error {
	return s.db.Close()
}

func openPool(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("ping: %w", err), db.Close())
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, errors.Join(fmt.Errorf("schema: %w", err), db.Close())
	}
	return db, nil
}

func NewStore(ctx context.Context, path string) (*Store, error) {
	db, err := openPool(ctx, path)
	if err != nil {
		return nil, err
	}

	store := &Store{db: db}

	stmts := []struct {
		db    *sql.DB
		dst   **sql.Stmt
		query string
	}{
		{db, &store.st_listUsers, q_listUsers},
		{db, &store.st_addUser, q_addUser},
		{db, &store.st_getUser, q_getUser},
		{db, &store.st_deleteUser, q_deleteUser},
		{db, &store.st_updateUserName, q_updateUserName},

		{db, &store.st_addPassword, q_addPassword},
		{db, &store.st_deletePassword, q_deletePassword},

		{db, &store.st_addTask, q_addTask},
		{db, &store.st_deleteTask, q_deleteTask},
		{db, &store.st_switchTask, q_switchTask},
		{db, &store.st_changeTask, q_changeTask},
		{db, &store.st_listTasks, q_listTasks},
		{db, &store.st_getTask, q_getTask},
		{db, &store.st_listUserTasks, q_listUserTasks},
		{db, &store.st_deleteAllUserTasks, q_deleteAllUserTasks},
	}
	for _, st := range stmts {
		if *st.dst, err = st.db.PrepareContext(ctx, st.query); err != nil {
			db.Close()
			return nil, errors.Join(fmt.Errorf("prepare %q: %w", st.query, err), db.Close())
		}
	}

	return store, nil
}

type User struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (s *Store) AddUser(ctx context.Context, name string, password_hash, salt []byte) (User, error) {
	var u User
	if err := s.st_addUser.QueryRowContext(ctx, name).Scan(&u.ID, &u.Name); err != nil {
		return User{}, fmt.Errorf("adding user: %w", err)
	}
	if err := s.addPassword(ctx, u.ID, password_hash, salt); err != nil {
		return User{}, errors.Join(err)
	}
	return u, nil
}

func (s *Store) GetUser(ctx context.Context, id int64) (User, error) {
	var u User
	if err := s.st_getUser.QueryRowContext(ctx, id).Scan(&u.ID, &u.Name); err != nil {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	ul := []User{}
	rows, err := s.st_listUsers.QueryContext(ctx)
	if err != nil {
		return []User{}, fmt.Errorf("failed to reach db: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Name); err != nil {
			return []User{}, fmt.Errorf("scanning user from list: %w", err)
		}
		ul = append(ul, u)
	}
	if err := rows.Err(); err != nil {
		return []User{}, err
	}
	return ul, nil
}

func checkDeletion(r sql.Result) error {
	n, err := r.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	r, err := s.st_deleteUser.ExecContext(ctx, id)
	if err := checkDeletion(r); err != nil {
		return err
	}
	if err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}
	return nil
}

func (s *Store) UpdateUserName(ctx context.Context, id int64, name string) (User, error) {
	var u User
	if err := s.st_updateUserName.QueryRowContext(ctx, name, id).Scan(&u.ID, &u.Name); err != nil {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (s *Store) addPassword(ctx context.Context, owner_id int64, password_hash, salt []byte) error {
	_, err := s.st_addPassword.ExecContext(ctx, owner_id, password_hash, salt)
	if err != nil {
		return fmt.Errorf("creating password: %w", err)
	}
	return nil
}

func (s *Store) deletePassword(ctx context.Context, owner_id int64) error {
	r, err := s.st_deletePassword.ExecContext(ctx, owner_id)
	if err := checkDeletion(r); err != nil {
		return err
	}
	if err != nil {
		return fmt.Errorf("deleting password: %w", err)
	}
	return nil
}

type Task struct {
	ID      int64  `json:"id"`
	OwnerID int64  `json:"owner_id"`
	Body    string `json:"body"`
	State   int    `json:"state"`
}

func (s *Store) CreateTask(ctx context.Context, ownerID int64, body string) (Task, error) {
	var t Task
	if err := s.st_addTask.QueryRowContext(ctx, ownerID, body).Scan(&t.ID, &t.OwnerID, &t.Body, &t.State); err != nil {
		return Task{}, fmt.Errorf("creating task: %w", err)
	}
	return t, nil
}

func (s *Store) DeleteTask(ctx context.Context, id int64, ownerID int64) error {
	r, err := s.st_deleteTask.ExecContext(ctx, id, ownerID)
	if err := checkDeletion(r); err != nil {
		return err
	}
	if err != nil {
		return fmt.Errorf("deleting task: %w", err)
	}
	return nil
}
func (s *Store) UpdateTask(ctx context.Context, id int64, ownerID int64, body string) (Task, error) {
	var t Task
	if err := s.st_changeTask.QueryRowContext(ctx, body, id, ownerID).Scan(&t.ID, &t.OwnerID, &t.Body, &t.State); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, fmt.Errorf("updating task: %w", err)
	}
	return t, nil
}
func (s *Store) SwitchTask(ctx context.Context, id int64, ownerID int64, state int) (Task, error) {
	var t Task
	if err := s.st_switchTask.QueryRowContext(ctx, state, id, ownerID).Scan(&t.ID, &t.OwnerID, &t.Body, &t.State); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, fmt.Errorf("updating task state: %w", err)
	}
	return t, nil
}
func (s *Store) ListTasks(ctx context.Context) ([]Task, error) {
	lt := []Task{}
	rows, err := s.st_listTasks.QueryContext(ctx)
	if err != nil {
		return []Task{}, fmt.Errorf("quering tasks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.OwnerID, &t.Body, &t.State); err != nil {
			return []Task{}, fmt.Errorf("scanning tasks: %w", err)
		}
		lt = append(lt, t)
	}
	if err := rows.Err(); err != nil {
		return []Task{}, err
	}
	return lt, nil

}
func (s *Store) GetTask(ctx context.Context, id int64, ownerID int64) (Task, error) {
	var t Task
	if err := s.st_getTask.QueryRowContext(ctx, id, ownerID).Scan(&t.ID, &t.OwnerID, &t.Body, &t.State); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, fmt.Errorf("getting task: %w", err)
	}
	return t, nil
}
func (s *Store) deleteAllUserTasks(ctx context.Context, ownerID int64) error {
	r, err := s.st_deleteAllUserTasks.ExecContext(ctx, ownerID)
	if err := checkDeletion(r); err != nil {
		return err
	}
	if err != nil {
		return fmt.Errorf("deleting user tasks: %w", err)
	}
	return nil
}
func (s *Store) ListUserTasks(ctx context.Context, ownerID int64) ([]Task, error) {
	lt := []Task{}
	rows, err := s.st_listUserTasks.QueryContext(ctx, ownerID)
	if err != nil {
		return []Task{}, fmt.Errorf("quering user tasks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.OwnerID, &t.Body, &t.State); err != nil {
			return []Task{}, fmt.Errorf("scanning user tasks: %w", err)
		}
		lt = append(lt, t)
	}
	if err := rows.Err(); err != nil {
		return []Task{}, err
	}
	return lt, nil
}
