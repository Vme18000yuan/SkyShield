package traffic

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteExchangesRemovesOnlySelectedBodiesAndLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"one", "two"} {
		path := filepath.Join("same.example", "GET", id)
		if err = os.MkdirAll(filepath.Join(dir, path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, path, "response.txt"), []byte(id), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = store.DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path) VALUES(?,1,'same.example','GET','/',?,200,'text/plain',0,3,?)`, id, "http://same.example/"+id, path); err != nil {
			t.Fatal(err)
		}
		if _, err = store.DB().Exec(`INSERT INTO exchange_bodies(id,req_head,req_body,resp_head,resp_body) VALUES(?,'GET / HTTP/1.1','','HTTP/1.1 200 OK',?)`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	n, err := store.DeleteExchanges([]string{"one"})
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	store.reaping.Wait()
	for _, table := range []string{"exchanges", "exchange_bodies"} {
		var n int
		if err = store.DB().QueryRow(fmt.Sprintf("SELECT count(*) FROM %s WHERE id='one'", table)).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	if _, err = os.Stat(filepath.Join(dir, "same.example", "GET", "one")); !os.IsNotExist(err) {
		t.Fatal("deleted file retained", err)
	}
	if content, err := os.ReadFile(filepath.Join(dir, "same.example", "GET", "two", "response.txt")); err != nil || string(content) != "two" {
		t.Fatal("unrelated traffic removed", err)
	}
	if n, err = store.DeleteExchanges([]string{"one"}); err != nil || n != 0 {
		t.Fatal("retry", n, err)
	}
}

func TestRecordDeletionRecoversFromSQLiteCommitBoundary(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			dir := t.TempDir()
			store, err := Open(dir, "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "fixture.test", "GET", "record")
			if err = os.MkdirAll(path, 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(path, "response.txt"), []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = store.DB().Exec(`INSERT INTO exchanges(id,ts,host,path) VALUES('record',1,'fixture.test','fixture.test/GET/record')`); err != nil {
				t.Fatal(err)
			}
			stage, moves, err := store.stageTrees([]string{path})
			if err != nil {
				t.Fatal(err)
			}
			if err = recordDeleteJournal(stage, moves, []string{"record"}); err != nil {
				t.Fatal(err)
			}
			if committed {
				if _, err = store.DB().Exec(`DELETE FROM exchanges WHERE id='record'`); err != nil {
					t.Fatal(err)
				}
			}
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = Open(dir, "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			_, err = os.Stat(path)
			if committed && !os.IsNotExist(err) || !committed && err != nil {
				t.Fatalf("committed=%v restored path error=%v", committed, err)
			}
			if _, err = os.Stat(stage); !os.IsNotExist(err) {
				t.Fatal("journal retained after recovery", err)
			}
		})
	}
}
