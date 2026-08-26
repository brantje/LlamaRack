package auth

import (
	"context"
	"errors"
	"strings"
)

type CreateUserInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role Role `json:"role"`
}

type UpdateUserInput struct {
	Role *Role `json:"role,omitempty"`
	Enabled *bool `json:"enabled,omitempty"`
	Password *string `json:"password,omitempty"`
}

func (s *Service) ListUsers(ctx context.Context) ([]User,error) {
	rows,err:=s.db.QueryContext(ctx,"SELECT id,username,role,enabled FROM users ORDER BY username");if err!=nil{return nil,err};defer rows.Close();var out []User;for rows.Next(){var u User;var enabled int;if err:=rows.Scan(&u.ID,&u.Username,&u.Role,&enabled);err!=nil{return nil,err};u.Enabled=enabled!=0;out=append(out,u)};return out,rows.Err()
}

func (s *Service) CreateUser(ctx context.Context,in CreateUserInput)(User,error){in.Username=strings.TrimSpace(in.Username);if len(in.Username)<2{return User{},errors.New("username must be at least 2 characters")};if len(in.Password)<10{return User{},errors.New("password must be at least 10 characters")};if !validRole(in.Role){return User{},errors.New("invalid role")};hash,err:=hashPassword(in.Password);if err!=nil{return User{},err};result,err:=s.db.ExecContext(ctx,"INSERT INTO users(username,password_hash,role) VALUES(?,?,?)",in.Username,hash,in.Role);if err!=nil{return User{},err};id,_:=result.LastInsertId();return User{ID:id,Username:in.Username,Role:in.Role,Enabled:true},nil}

func (s *Service) UpdateUser(ctx context.Context,id int64,in UpdateUserInput)(User,error){var current User;var enabled int;if err:=s.db.QueryRowContext(ctx,"SELECT id,username,role,enabled FROM users WHERE id=?",id).Scan(&current.ID,&current.Username,&current.Role,&enabled);err!=nil{return User{},err};current.Enabled=enabled!=0;if in.Role!=nil{if !validRole(*in.Role){return User{},errors.New("invalid role")};current.Role=*in.Role};if in.Enabled!=nil{current.Enabled=*in.Enabled};if (current.Role!=RoleAdmin||!current.Enabled){var admins int;if err:=s.db.QueryRowContext(ctx,"SELECT COUNT(*) FROM users WHERE role='admin' AND enabled=1 AND id<>?",id).Scan(&admins);err!=nil{return User{},err};if admins==0{return User{},errors.New("cannot remove or disable the final administrator")}}
	tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return User{},err};defer tx.Rollback();if _,err:=tx.ExecContext(ctx,"UPDATE users SET role=?,enabled=?,updated_at=CURRENT_TIMESTAMP WHERE id=?",current.Role,boolToInt(current.Enabled),id);err!=nil{return User{},err};if in.Password!=nil{if len(*in.Password)<10{return User{},errors.New("password must be at least 10 characters")};hash,err:=hashPassword(*in.Password);if err!=nil{return User{},err};if _,err:=tx.ExecContext(ctx,"UPDATE users SET password_hash=? WHERE id=?",hash,id);err!=nil{return User{},err};if _,err:=tx.ExecContext(ctx,"DELETE FROM sessions WHERE user_id=?",id);err!=nil{return User{},err}};if !current.Enabled{if _,err:=tx.ExecContext(ctx,"DELETE FROM sessions WHERE user_id=?",id);err!=nil{return User{},err}};if err:=tx.Commit();err!=nil{return User{},err};return current,nil}

func validRole(role Role)bool{return role==RoleAdmin||role==RoleOperator||role==RoleReadonly}
func boolToInt(v bool)int{if v{return 1};return 0}
