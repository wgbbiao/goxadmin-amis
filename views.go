package goxadmin

import (
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/iris-contrib/middleware/jwt"
	"github.com/kataras/iris/v12"
	"github.com/unknwon/com"
	"github.com/wxnacy/wgo/arrays"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func getToken(_ iris.Context, u User) (tokenString string) {
	claim := jwt.MapClaims{
		"exp": time.Now().Unix() + JwtTimeOut,
		"uid": u.ID,
	}

	accessToken := jwt.NewTokenWithClaims(jwt.SigningMethodHS256, claim)
	tokenString, _ = accessToken.SignedString([]byte(JwtKey))
	return
}

//Login 用户登录
func Login(c iris.Context) {
	type Form struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	var form Form
	var u User
	if err := c.ReadJSON(&form); err != nil {
		c.JSON(iris.Map{
			"status": 1,
			"msg":    FormReadError,
		})
	} else {
		if db := u.GetByUsername(form.Username); db.Error == gorm.ErrRecordNotFound {
			c.JSON(iris.Map{
				"status": 1,
				"msg":    UserDoesNotExist,
			})
		} else {
			if u.CheckPassword(form.Password) {
				tokenString := getToken(c, u)
				u.UpdateInfo(map[string]interface{}{
					"last_login_at": time.Now(),
				})
				c.JSON(iris.Map{
					"status": 0,
					"data": iris.Map{
						"token":    tokenString,
						"username": u.Username,
					},
				})
			} else {
				c.StatusCode(iris.StatusBadRequest)
				c.JSON(iris.Map{
					"status": 1,
					"msg":    UserPasswordError,
				})
			}
		}
	}
}

//ChangePassword 修改个人密码
func ChangePassword(c iris.Context) {
	u := c.Values().Get("u").(User)
	if err := c.ReadJSON(&u); err == nil {
		if err = Validate.Struct(u); err == nil {
			u.SetPassword() //密码加密
			Db.Model(&u).UpdateColumns(User{Password: u.Password, Salt: u.Salt})
			c.JSON(iris.Map{
				"status": 0,
			})
		} else {
			c.StatusCode(iris.StatusBadRequest)
			c.JSON(iris.Map{
				"status": 1,
				"msg":    ValidateError,
			})
		}
	} else {
		c.StatusCode(iris.StatusBadRequest)
		c.JSON(iris.Map{
			"status": 1,
			"msg":    FormReadError,
		})
	}
}

//RefreshJwt 刷新jwt
func RefreshJwt(c iris.Context) {
	u := c.Values().Get("u").(User)
	tokenString := getToken(c, u)
	c.JSON(iris.Map{
		"status": 0,
		"data": iris.Map{
			"token": tokenString,
			// "username": u.Username,
		},
	})
}

//GetInfo 取得用户信息
func GetInfo(c iris.Context) {
	u := c.Values().Get("u").(User)
	permissions := u.GetPermissionInfo()
	c.JSON(iris.Map{
		"status": 0,
		"data": iris.Map{
			"username":    u.Username,
			"isSuper":     u.IsSuper,
			"permissions": permissions,
		},
	})
}

var jwtCfg = jwt.Config{
	ValidationKeyGetter: func(token *jwt.Token) (interface{}, error) {
		return []byte(JwtKey), nil
	},
	SigningMethod: jwt.SigningMethodHS256,
	ErrorHandler:  OnJwtError,
}

var myJwtMiddleware = jwt.New(jwtCfg)

//OnJwtError jwt error
func OnJwtError(ctx iris.Context, err error) {
	ctx.StatusCode(iris.StatusUnauthorized)
	ctx.JSON(iris.Map{
		"status": "fail",
		"info":   err,
		"msg":    TokenIsExpired,
	})
}

//CheckJWTAndSetUser 检查jwt并把User放到Values
func CheckJWTAndSetUser(ctx iris.Context) {
	if err := myJwtMiddleware.CheckJWT(ctx); err != nil {
		myJwtMiddleware.Config.ErrorHandler(ctx, err)
		return
	}
	// If everything ok then call next.
	if ctx.GetStatusCode() != iris.StatusUnauthorized {
		var u User
		x, _ := ctx.Values().Get("jwt").(*jwt.Token).Claims.(jwt.MapClaims)
		if rt := u.GetUserByID(int(x["uid"].(float64))); !(rt.Error == gorm.ErrRecordNotFound) && rt.Error == nil {
			bl := true
			if ctx.Params().Get("model") != "" {
				config := GetConfig(ctx.Params().Get("model"), ctx.Params().GetString("table"))
				bl = HasPermissionForModel(&u, config.Model, GetActionByMethod(ctx.Method()))
			}
			if bl {
				ctx.Values().Set("u", u)
				ctx.Next()
			} else {
				ctx.JSON(iris.Map{
					"status": HTTPForbidden,
					"msg":    UserNoPermission,
				})
			}
		} else {
			ctx.JSON(iris.Map{
				"status": 1,
				"msg":    UserDoesNotExist,
			})
		}
	}
}

// ListHandel ListHandel
func ListHandel(ctx iris.Context) {
	config := GetConfig(ctx.Params().Get("model"), ctx.Params().Get("table"))
	if arrays.ContainsString(config.DisableAction, "list") > -1 {

	} else {
		rs := NewSlice(config.Model)
		page := com.StrTo(ctx.URLParam("page")).MustInt()
		all, _ := ctx.URLParamBool("__all__")
		if page == 0 {
			page = 1
		}
		limit := config.PageSize

		if all {
			limit = 999999
		} else {
			if ctx.URLParamExists("page_size") {
				limit, _ = ctx.URLParamInt("page_size")
			}
			if limit == 0 {
				limit = 20
			}
		}
		offset := (page - 1) * limit
		params := ctx.URLParams()
		// 查询前的处理查询条件
		if config.BeforeListQuery != nil {
			config.BeforeListQuery(&params)
		}
		cnt := int64(0)

		err := Db.Model(config.Model).Set("gorm:auto_preload", false).Scopes(MapToWhere(params, config)).
			Count(&cnt).
			Limit(limit).
			Offset(offset).
			Find(rs).Error
		if err == nil {
			ctx.JSON(iris.Map{
				"status": 0,
				"data": iris.Map{
					"list":  rs,
					"total": cnt,
				},
			})
		} else {
			ctx.JSON(iris.Map{
				"status": 1,
				"msg":    "save error",
			})
		}
	}
}

//DetailHandel 详情页
func DetailHandel(ctx iris.Context) {
	id, _ := ctx.Params().GetInt("id")
	config := GetConfig(ctx.Params().Get("model"), ctx.Params().Get("table"))
	if arrays.ContainsString(config.DisableAction, "detail") > -1 {

	} else {
		obj := GetVal(config.Model)
		params := ctx.URLParams()
		if err := Db.Scopes(MapToWhere(params, config)).First(obj, id).Error; err == nil {
			ctx.JSON(iris.Map{
				"status": 0,
				"data":   obj,
			})
		} else {
			ctx.JSON(iris.Map{
				"status": 1,
				"msg":    "save error",
			})
		}
	}
}

//PostHandel 添加记录
func PostHandel(ctx iris.Context) {
	config := GetConfig(ctx.Params().Get("model"), ctx.Params().Get("table"))
	if arrays.ContainsString(config.DisableAction, "create") > -1 {

	} else {
		obj := GetVal(config.Model)
		if err := ctx.ReadJSON(&obj); err == nil {
			if err = Validate.Struct(obj); err == nil {
				if config.BeforeSave != nil {
					config.BeforeSave(obj)
				}
				if err = Db.Create(obj).Error; err == nil {
					ctx.JSON(iris.Map{
						"status": 0,
						"data":   obj,
					})
				} else {
					ctx.JSON(iris.Map{
						"status": 1,
						"msg":    DBError,
					})
				}
			} else {
				ctx.JSON(iris.Map{
					"status": 1,
					"msg":    ValidateError,
				})
			}
		} else {
			ctx.JSON(iris.Map{
				"status": 1,
				"msg":    FormReadError,
			})
		}
	}
}

//UpdateHandel 修改记录
func UpdateHandel(ctx iris.Context) {
	config := GetConfig(ctx.Params().Get("model"), ctx.Params().Get("table"))
	if arrays.ContainsString(config.DisableAction, "update") > -1 {

	} else {
		obj := GetVal(config.Model)
		id, _ := ctx.Params().GetInt("id")
		Db.First(obj, id)
		t := reflect.TypeOf(obj).Elem()
		// 删除多对多的关系，然后重新添加
		for k := 0; k < t.NumField(); k++ {
			field := t.Field(k)
			if strings.Contains(field.Tag.Get("gorm"), "many2many") {
				Db.Model(obj).Association(t.Field(k).Name).Clear()
			}
		}

		if err := ctx.ReadJSON(&obj); err == nil {
			if config.BeforeSave != nil {
				config.BeforeSave(obj)
			}
			if Db.Save(obj).Error == nil {
				sc, _ := schema.Parse(obj, &sync.Map{}, Db.NamingStrategy)
				for _, f := range sc.Relationships.HasMany {
					d := reflect.Indirect(reflect.ValueOf(obj))
					ff := d.FieldByName(f.Name)
					Db.Model(&obj).Association(f.Name).Replace(&ff)
				}
				for _, f := range sc.Relationships.Many2Many {
					d := reflect.Indirect(reflect.ValueOf(obj))
					ff := d.FieldByName(f.Name)
					Db.Model(&obj).Association(f.Name).Replace(&ff)
				}
				ctx.JSON(iris.Map{
					"status": 0,
				})
			} else {
				ctx.JSON(iris.Map{
					"status": 1,
					"msg":    DBError,
				})
			}
		} else {
			ctx.JSON(iris.Map{
				"status": 1,
				"msg":    FormReadError,
			})
		}
	}
}

//DeleteHandel 删除记录
func DeleteHandel(ctx iris.Context) {
	id, _ := ctx.Params().GetInt("id")
	config := GetConfig(ctx.Params().Get("model"), ctx.Params().Get("table"))
	if arrays.ContainsString(config.DisableAction, "delete") > -1 {

	} else {
		obj := GetVal(config.Model)
		// 查询记录
		if err := Db.First(obj, id).Error; err == nil {
			if Db.Delete(obj).Error == nil {
				ctx.JSON(iris.Map{
					"status": 0,
					"data":   iris.Map{},
				})
			} else {
				ctx.JSON(iris.Map{
					"status": 1,
					"msg":    DBError,
				})
			}
		} else {
			ctx.JSON(iris.Map{
				"status": 1,
				"msg":    DBError,
			})
		}
	}
}

// BatchUpdateHandel 批量更新记录
func BatchUpdateHandel(ctx iris.Context) {
	ids := ctx.URLParam("ids")
	config := GetConfig(ctx.Params().Get("model"), ctx.Params().Get("table"))
	if arrays.ContainsString(config.DisableAction, "delete") > -1 {

	} else {
		succ := 0
		fail := 0
		var updateJSON map[string]interface{}
		if err := ctx.ReadJSON(&updateJSON); err == nil {
			Db.Transaction(func(tx *gorm.DB) error {
				for _, id := range strings.Split(ids, ",") {
					obj := GetVal(config.Model)
					if db := tx.Model(obj).Where("id = ?", com.StrTo(id).MustInt()).Not(updateJSON).Updates(updateJSON); db.Error == nil && db.RowsAffected > 0 {
						succ++
					} else {
						fail++
					}
				}
				return nil
			})
			ctx.JSON(iris.Map{
				"status": 0,
				"data": iris.Map{
					"fail": fail,
					"succ": succ,
				},
			})
		} else {
			ctx.JSON(iris.Map{
				"status": 1,
			})
		}
	}
}

//BatchDeleteHandel 批量删除记录
func BatchDeleteHandel(ctx iris.Context) {
	ids := ctx.URLParam("ids")
	config := GetConfig(ctx.Params().Get("model"), ctx.Params().Get("table"))
	if arrays.ContainsString(config.DisableAction, "delete") > -1 {

	} else {
		succ := 0
		fail := 0
		for _, id := range strings.Split(ids, ",") {
			obj := GetVal(config.Model)
			if err := Db.First(obj, com.StrTo(id).MustInt()).Error; err == nil {
				if Db.Delete(obj).Error == nil {
					succ++
				} else {
					fail++
				}
			} else {
				fail++
			}
		}
		ctx.JSON(iris.Map{
			"status": 0,
			"fail":   fail,
			"succ":   succ,
		})
	}
}
