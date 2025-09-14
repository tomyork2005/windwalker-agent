package main

import (
	badger "github.com/dgraph-io/badger/v4"
	"log"
	"time"
)

func main() {
	db, err := badger.Open(badger.DefaultOptions("tmp/badger"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	err = db.Update(func(txn *badger.Txn) error {
		e := badger.NewEntry([]byte("user:123"), []byte("premium"))
		e.WithTTL(10 * time.Second) // ключ «живёт» 10 секунд
		return txn.SetEntry(e)
	})
	if err != nil {
		log.Fatal(err)
	}

	err = db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte("user:123"))
		if err != nil {
			return err
		}
		val, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		log.Println("Значение:", string(val))
		return nil
	})
	if err != nil {
		log.Println("Ошибка чтения:", err)
	}

	time.Sleep(12 * time.Second)

	err = db.View(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte("user:123"))
		if err == badger.ErrKeyNotFound {
			log.Println("Ключ протух и удалён!")
			return nil
		}
		return err
	})
	if err != nil {
		log.Fatal(err)
	}
}
