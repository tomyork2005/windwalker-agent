package internal

import (
	"errors"
	badger "github.com/dgraph-io/badger/v4"
	"time"
)

type BadgerStorage struct {
	db  *badger.DB
	ttl time.Duration
}

func NewBadgerStorage(path string, ttl time.Duration) (*BadgerStorage, error) {
	db, err := badger.Open(badger.DefaultOptions(path).WithLoggingLevel(badger.WARNING))
	if err != nil {
		return nil, err
	}
	return &BadgerStorage{
		db:  db,
		ttl: ttl,
	}, nil
}

func (s *BadgerStorage) Add(id string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		e := badger.NewEntry([]byte(id), []byte("1")).WithTTL(s.ttl)
		return txn.SetEntry(e)
	})
}

func (s *BadgerStorage) Has(id string) (bool, error) {
	err := s.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte(id))
		return err
	})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, badger.ErrKeyNotFound) {
		return false, nil
	}
	return false, err
}

func (s *BadgerStorage) Close() error {
	return s.db.Close()
}

// периодически:
func (s *BadgerStorage) RunGC() {
	for {
		// 0.5 => порог экономии; вызывай раз в N минут в отдельной горутине
		if err := s.db.RunValueLogGC(0.5); err != nil {
			break
		}
	}
}
