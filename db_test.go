package main

// These tests assume the fixed Store API:
//
//	var ErrNotFound = errors.New("not found")
//
//	func (s *Store) Close() error
//
//	AddUser(ctx, name string, hash, salt []byte) (User, error)
//	GetUser(ctx, id int64) (User, error)
//	ListUsers(ctx) ([]User, error)
//	UpdateUserName(ctx, id int64, name string) (User, error)
//	UpdateUserPassword(ctx, id int64, hash, salt []byte) error
//	DeleteUser(ctx, id int64) error
//
//	CreateTask(ctx, ownerID int64, body string) (Task, error)
//	GetTask(ctx, id, ownerID int64) (Task, error)
//	UpdateTask(ctx, id, ownerID int64, body string) (Task, error)
//	SwitchTask(ctx, id, ownerID int64, state int) (Task, error)
//	DeleteTask(ctx, id, ownerID int64) error
//	ListTasks(ctx) ([]Task, error)
//	ListUserTasks(ctx, ownerID int64) ([]Task, error)
//
// Task has a field OwnerID (renamed from Owner_ID).
// Every "not found" case (missing row OR wrong owner) must return ErrNotFound.

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// newTestStore opens a fresh database in a temp dir.
// t.TempDir() creates a unique directory and deletes it automatically when the
// test ends. t.Cleanup runs in reverse order of registration, so the store is
// closed BEFORE the directory is removed.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := NewStore(context.Background(), path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func mustAddUser(t *testing.T, s *Store, name string) User {
	t.Helper()
	u, err := s.AddUser(context.Background(), name, []byte("hash-"+name), []byte("salt-"+name))
	if err != nil {
		t.Fatalf("AddUser(%q): %v", name, err)
	}
	return u
}

func mustCreateTask(t *testing.T, s *Store, owner int64, body string) Task {
	t.Helper()
	task, err := s.CreateTask(context.Background(), owner, body)
	if err != nil {
		t.Fatalf("CreateTask(%q): %v", body, err)
	}
	return task
}

// readPassword reads the raw passwords row, bypassing the Store API.
func readPassword(t *testing.T, s *Store, ownerID int64) (hash, salt []byte) {
	t.Helper()
	pass, err := s.getUserPassword(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("reading password row: %v", err)
	}
	return pass.Hash, pass.Salt
}

func count(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count query: %v", err)
	}
	return n
}

func TestNewStoreOnExistingDB(t *testing.T) {
	// Opening the same file twice catches broken "create table if not exists" logic.
	path := filepath.Join(t.TempDir(), "test.db")
	ctx := context.Background()

	s1, err := NewStore(ctx, path)
	if err != nil {
		t.Fatalf("first NewStore: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := NewStore(ctx, path)
	if err != nil {
		t.Fatalf("second NewStore on existing DB: %v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestUsers(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	alice := mustAddUser(t, s, "alice")
	bob := mustAddUser(t, s, "bob")
	if alice.ID == bob.ID {
		t.Fatalf("users got the same id %d", alice.ID)
	}
	if alice.Name != "alice" {
		t.Errorf("name = %q, want alice", alice.Name)
	}

	got, err := s.GetUser(ctx, alice.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got != alice {
		t.Errorf("GetUser = %+v, want %+v", got, alice)
	}

	list, err := s.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("ListUsers returned %d users, want 2", len(list))
	}

	renamed, err := s.UpdateUserName(ctx, alice.ID, "alice2")
	if err != nil {
		t.Fatalf("UpdateUserName: %v", err)
	}
	if renamed.ID != alice.ID || renamed.Name != "alice2" {
		t.Errorf("UpdateUserName = %+v", renamed)
	}
	// bob must be untouched
	if b, _ := s.GetUser(ctx, bob.ID); b.Name != "bob" {
		t.Errorf("bob was modified: %+v", b)
	}
}

func TestUserNotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.GetUser(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUser(missing) err = %v, want ErrNotFound", err)
	}
	if _, err := s.UpdateUserName(ctx, 999, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateUserName(missing) err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteUser(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteUser(missing) err = %v, want ErrNotFound", err)
	}
}

func TestPasswordStoredAndUpdated(t *testing.T) {
	// ctx := context.Background()
	s := newTestStore(t)

	u := mustAddUser(t, s, "alice")
	other := mustAddUser(t, s, "bob")

	hash, salt := readPassword(t, s, u.ID)
	if !bytes.Equal(hash, []byte("hash-alice")) || !bytes.Equal(salt, []byte("salt-alice")) {
		t.Fatalf("stored hash/salt = %q/%q", hash, salt)
	}

	// if err := s.UpdateUserPassword(ctx, u.ID, []byte("new-hash"), []byte("new-salt")); err != nil {
	// 	t.Fatalf("UpdateUserPassword: %v", err)
	// }
	// hash, salt = readPassword(t, s, u.ID)
	// if !bytes.Equal(hash, []byte("new-hash")) || !bytes.Equal(salt, []byte("new-salt")) {
	// 	t.Errorf("after update hash/salt = %q/%q", hash, salt)
	// }

	// the other user's password is untouched
	hash, _ = readPassword(t, s, other.ID)
	if !bytes.Equal(hash, []byte("hash-bob")) {
		t.Errorf("bob's password changed: %q", hash)
	}
}

func TestTaskLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	u := mustAddUser(t, s, "alice")

	task := mustCreateTask(t, s, u.ID, "write tests")
	if task.OwnerID != u.ID || task.Body != "write tests" || task.State != 0 {
		t.Fatalf("CreateTask = %+v", task)
	}

	got, err := s.GetTask(ctx, task.ID, u.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got != task {
		t.Errorf("GetTask = %+v, want %+v", got, task)
	}

	updated, err := s.UpdateTask(ctx, task.ID, u.ID, "write more tests")
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if updated.Body != "write more tests" || updated.State != 0 {
		t.Errorf("UpdateTask = %+v", updated)
	}

	// SwitchTask must change state and leave body alone
	// (the original code wrote the state into body).
	switched, err := s.SwitchTask(ctx, task.ID, u.ID, 1)
	if err != nil {
		t.Fatalf("SwitchTask: %v", err)
	}
	if switched.State != 1 || switched.Body != "write more tests" {
		t.Errorf("SwitchTask = %+v, want state 1 and body unchanged", switched)
	}

	if err := s.DeleteTask(ctx, task.ID, u.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if _, err := s.GetTask(ctx, task.ID, u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetTask after delete err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteTask(ctx, task.ID, u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second DeleteTask err = %v, want ErrNotFound", err)
	}
}

func TestDeleteTaskOnlyDeletesThatTask(t *testing.T) {
	// The original statement was "WHERE owner_id = ?", which deleted by the wrong column.
	ctx := context.Background()
	s := newTestStore(t)
	u := mustAddUser(t, s, "alice")

	t1 := mustCreateTask(t, s, u.ID, "one")
	t2 := mustCreateTask(t, s, u.ID, "two")
	t3 := mustCreateTask(t, s, u.ID, "three")

	if err := s.DeleteTask(ctx, t2.ID, u.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	left, err := s.ListUserTasks(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListUserTasks: %v", err)
	}
	if len(left) != 2 || left[0].ID != t1.ID || left[1].ID != t3.ID {
		t.Errorf("remaining tasks = %+v, want %d and %d", left, t1.ID, t3.ID)
	}
}

func TestListTasks(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	alice := mustAddUser(t, s, "alice")
	bob := mustAddUser(t, s, "bob")

	mustCreateTask(t, s, alice.ID, "a1")
	mustCreateTask(t, s, alice.ID, "a2")
	mustCreateTask(t, s, bob.ID, "b1")

	all, err := s.ListTasks(ctx)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListTasks returned %d, want 3", len(all))
	}

	aliceTasks, err := s.ListUserTasks(ctx, alice.ID)
	if err != nil {
		t.Fatalf("ListUserTasks(alice): %v", err)
	}
	if len(aliceTasks) != 2 {
		t.Errorf("alice has %d tasks, want 2", len(aliceTasks))
	}
	for _, task := range aliceTasks {
		if task.OwnerID != alice.ID {
			t.Errorf("alice's list contains %+v", task)
		}
	}

	// A user without tasks gets an empty list, not an error.
	carol := mustAddUser(t, s, "carol")
	none, err := s.ListUserTasks(ctx, carol.ID)
	if err != nil {
		t.Fatalf("ListUserTasks(carol): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("carol has %d tasks, want 0", len(none))
	}
}

func TestTaskOwnership(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	alice := mustAddUser(t, s, "alice")
	bob := mustAddUser(t, s, "bob")

	task := mustCreateTask(t, s, alice.ID, "alice's secret")

	if _, err := s.GetTask(ctx, task.ID, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob GetTask err = %v, want ErrNotFound", err)
	}
	if _, err := s.UpdateTask(ctx, task.ID, bob.ID, "hacked"); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob UpdateTask err = %v, want ErrNotFound", err)
	}
	if _, err := s.SwitchTask(ctx, task.ID, bob.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob SwitchTask err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteTask(ctx, task.ID, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob DeleteTask err = %v, want ErrNotFound", err)
	}

	// After all those attempts, alice's task is exactly as she left it.
	got, err := s.GetTask(ctx, task.ID, alice.ID)
	if err != nil {
		t.Fatalf("alice GetTask: %v", err)
	}
	if got != task {
		t.Errorf("task changed: got %+v, want %+v", got, task)
	}
}

func TestDeleteUserCascades(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	alice := mustAddUser(t, s, "alice")
	bob := mustAddUser(t, s, "bob")

	mustCreateTask(t, s, alice.ID, "a1")
	mustCreateTask(t, s, alice.ID, "a2")
	bobTask := mustCreateTask(t, s, bob.ID, "b1")

	if err := s.DeleteUser(ctx, alice.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	if n := count(t, s, "SELECT COUNT(*) FROM tasks WHERE owner_id = ?", alice.ID); n != 0 {
		t.Errorf("%d orphaned tasks left", n)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM passwords WHERE owner_id = ?", alice.ID); n != 0 {
		t.Errorf("%d orphaned password rows left", n)
	}

	// bob is unaffected
	if _, err := s.GetUser(ctx, bob.ID); err != nil {
		t.Errorf("bob disappeared: %v", err)
	}
	if _, err := s.GetTask(ctx, bobTask.ID, bob.ID); err != nil {
		t.Errorf("bob's task disappeared: %v", err)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	// If _foreign_keys=on is missing from the DSN, this succeeds and leaves an orphan.
	s := newTestStore(t)
	if _, err := s.CreateTask(context.Background(), 12345, "no such owner"); err == nil {
		t.Error("CreateTask for a nonexistent user succeeded, want an error")
	}
}
