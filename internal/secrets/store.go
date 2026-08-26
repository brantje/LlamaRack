package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Store struct{db *sql.DB;aead cipher.AEAD}

func New(db *sql.DB,dataDir string)(*Store,error){keyPath:=filepath.Join(dataDir,"master.key");key,err:=loadOrCreateKey(keyPath);if err!=nil{return nil,err};block,err:=aes.NewCipher(key);if err!=nil{return nil,err};aead,err:=cipher.NewGCM(block);if err!=nil{return nil,err};return &Store{db:db,aead:aead},nil}
func(s *Store)Set(ctx context.Context,name,value string)error{if name==""{return errors.New("secret name required")};nonce:=make([]byte,s.aead.NonceSize());if _,err:=io.ReadFull(rand.Reader,nonce);err!=nil{return err};sealed:=s.aead.Seal(nil,nonce,[]byte(value),[]byte(name));blob:=append(nonce,sealed...);_,err:=s.db.ExecContext(ctx,`INSERT INTO secrets(name,ciphertext,updated_at) VALUES(?,?,CURRENT_TIMESTAMP) ON CONFLICT(name) DO UPDATE SET ciphertext=excluded.ciphertext,updated_at=CURRENT_TIMESTAMP`,name,blob);return err}
func(s *Store)Get(ctx context.Context,name string)(string,bool,error){var blob []byte;err:=s.db.QueryRowContext(ctx,"SELECT ciphertext FROM secrets WHERE name=?",name).Scan(&blob);if errors.Is(err,sql.ErrNoRows){return "",false,nil};if err!=nil{return "",false,err};n:=s.aead.NonceSize();if len(blob)<n{return "",false,errors.New("encrypted secret is corrupt")};plaintext,err:=s.aead.Open(nil,blob[:n],blob[n:],[]byte(name));if err!=nil{return "",false,fmt.Errorf("decrypt secret: %w",err)};return string(plaintext),true,nil}
func(s *Store)Delete(ctx context.Context,name string)error{_,err:=s.db.ExecContext(ctx,"DELETE FROM secrets WHERE name=?",name);return err}
func(s *Store)Configured(ctx context.Context,name string)(bool,error){var count int;err:=s.db.QueryRowContext(ctx,"SELECT COUNT(*) FROM secrets WHERE name=?",name).Scan(&count);return count>0,err}

func loadOrCreateKey(path string)([]byte,error){if key,err:=os.ReadFile(path);err==nil{if len(key)!=32{return nil,errors.New("master.key must contain exactly 32 bytes")};return key,nil}else if !errors.Is(err,os.ErrNotExist){return nil,err};key:=make([]byte,32);if _,err:=io.ReadFull(rand.Reader,key);err!=nil{return nil,err};if err:=os.MkdirAll(filepath.Dir(path),0o750);err!=nil{return nil,err};file,err:=os.OpenFile(path,os.O_WRONLY|os.O_CREATE|os.O_EXCL,0o600);if err!=nil{return nil,err};if _,err:=file.Write(key);err!=nil{file.Close();return nil,err};if err:=file.Sync();err!=nil{file.Close();return nil,err};if err:=file.Close();err!=nil{return nil,err};return key,nil}
