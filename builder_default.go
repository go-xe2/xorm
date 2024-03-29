package xorm

import (
	"errors"
	"fmt"
	"github.com/go-xe2/xorm/sqlw"
	"github.com/gohouse/gocar/structEngin"
	"github.com/gohouse/t"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

var operator = []string{"=", ">", "<", "!=", "<>", ">=", "<=", "like", "not like",
	"in", "not in", "between", "not between"}

type BuilderDefault struct {
	IOrm
	operator    []string
	placeholder int
	driver      string
}

var onceBuilderDefault sync.Once
var builderDefault *BuilderDefault

func NewBuilderDefault(o IOrm) *BuilderDefault {
	onceBuilderDefault.Do(func() {
		builderDefault = new(BuilderDefault)
		builderDefault.operator = operator
		builderDefault.driver = "mysql"
	})
	builderDefault.IOrm = o
	// 每次使用的时候, 重置为0, 方便pg的占位符使用
	builderDefault.placeholder = 0
	return builderDefault
}

// SetDriver 设置驱动, 方便获取占位符使用
func (b *BuilderDefault) SetDriver(dr string) *BuilderDefault {
	b.driver = dr
	return b
}

// GetPlaceholder 获取占位符
func (b *BuilderDefault) GetPlaceholder() (phstr string) {
	switch b.driver {
	case "postgres":
		withLockContext(func() {
			ph := b.placeholder + 1
			phstr = fmt.Sprintf("$%v", ph)
			b.placeholder = ph
		})
	default:
		phstr = "?"
	}
	return
}

func (b *BuilderDefault) BuildQuery() (sqlStr string, args []interface{}, err error) {
	//b.IOrm = o
	join, err := b.BuildJoin()
	if err != nil {
		b.IOrm.GetISession().GetIEngin().GetLogger().Error(err.Error())
		return
	}
	where, err := b.BuildWhere()
	if err != nil {
		b.IOrm.GetISession().GetIEngin().GetLogger().Error(err.Error())
		return
	}
	sqlStr = fmt.Sprintf("SELECT %s%s FROM %s%s%s%s%s%s%s%s",
		b.BuildDistinct(), b.BuildFields(), b.BuildTable(), join, where,
		b.BuildGroup(), b.BuildHaving(), b.BuildOrder(), b.BuildLimit(), b.BuildOffset())

	//args = b.bindParams
	args = b.IOrm.GetBindValues()
	return
}

// BuildExecut : build execute query string
func (b *BuilderDefault) BuildExecute(operType string) (sqlStr string, args []interface{}, err error) {
	// insert : {"name":"fizz, "website":"fizzday.net"} or {{"name":"fizz2", "website":"www.fizzday.net"}, {"name":"fizz", "website":"fizzday.net"}}}
	// update : {"name":"fizz", "website":"fizzday.net"}
	// delete : ...
	//b.IOrm = o
	var update, insertkey, insertval string
	if operType != "delete" {
		if b.IOrm.GetData() == nil {
			err = errors.New("insert,update请传入数据操作")
			b.IOrm.GetISession().GetIEngin().GetLogger().Error(err.Error())
			return
		}
		update, insertkey, insertval = b.BuildData(operType)
	}

	where, err := b.BuildWhere()
	if err != nil {
		b.IOrm.GetISession().GetIEngin().GetLogger().Error(err.Error())
		return
	}
	switch operType {
	case "insert":
		sqlStr = fmt.Sprintf("INSERT INTO %s (%s) VALUES %s", b.BuildTable(), insertkey, insertval)
	case "update":
		if where == "" && b.IOrm.GetForce() == false {
			err = errors.New("出于安全考虑, update时where条件不能为空, 如果真的不需要where条件, 请使用Force()(如: db.xxx.Force().Update())")
			b.IOrm.GetISession().GetIEngin().GetLogger().Error(err.Error())
			return
		}
		sqlStr = fmt.Sprintf("UPDATE %s SET %s%s", b.BuildTable(), update, where)
	case "delete":
		if where == "" && b.IOrm.GetForce() == false {
			err = errors.New("出于安全考虑, delete时where条件不能为空, 如果真的不需要where条件, 请使用Force()(如: db.xxx.Force().Delete())")
			b.IOrm.GetISession().GetIEngin().GetLogger().Error(err.Error())
			return
		}
		sqlStr = fmt.Sprintf("DELETE FROM %s%s", b.BuildTable(), where)
	}

	args = b.IOrm.GetBindValues()
	return
}

// BuildData : build inert or update data
func (b *BuilderDefault) BuildData(operType string) (string, string, string) {
	data := b.IOrm.GetData()
	ref := reflect.Indirect(reflect.ValueOf(data))

	switch ref.Kind() {
	case reflect.String:
		return b.parseStringData(operType, []string{ data.(string) })
	case reflect.Struct:
		return b.parseData(operType, structEngin.New().SetExtraCols(b.IOrm.GetExtraCols()).StructContent2Map(data))
	case reflect.Map:
		var tmp = []map[string]interface{}{t.New(data).MapStringInterface()}
		return b.parseData(operType, tmp)
	case reflect.Slice:
		switch ref.Type().Elem().Kind() {
		case reflect.String:
			return b.parseStringData(operType, t.New(data).SliceString())
		case reflect.Struct:
			return b.parseData(operType, structEngin.New().SetExtraCols(b.IOrm.GetExtraCols()).StructContent2Map(data))
		case reflect.Map:
			return b.parseData(operType, t.New(data).SliceMapStringInterface())
		}
	}
	return "", "", ""
}
func (b *BuilderDefault) BuildData2(operType string) (string, string, string) {
	// insert
	var dataFields []string
	var dataValues []string
	// update or delete
	var dataObj []string

	data := b.IOrm.GetData()

	switch data.(type) {
	case string:
		dataObj = append(dataObj, data.(string))
	case []map[string]interface{}: // insert multi datas ([]map[string]interface{})
		sliceData := t.New(data).Slice()
		for key, _ := range sliceData[0].MapString() {
			if inArray(key, dataFields) == false {
				dataFields = append(dataFields, key)
			}
		}
		for _, itemT := range sliceData {
			item := itemT.MapString()
			var dataValuesSub []string
			for _, key := range dataFields {
				if item[key] == nil {
					dataValuesSub = append(dataValuesSub, "null")
				} else {
					dataValuesSub = append(dataValuesSub, b.GetPlaceholder())
					b.IOrm.SetBindValues(item[key])
				}
			}
			dataValues = append(dataValues, "("+strings.Join(dataValuesSub, ",")+")")
		}
	case map[string]interface{}: // update or insert (map[string]interface{})
		var dataValuesSub []string
		for key, val := range t.New(data).MapString() {
			if operType == "insert" {
				// insert
				dataFields = append(dataFields, key)
				if val.Interface() == nil {
					dataValuesSub = append(dataValuesSub, "null")
				} else {
					dataValuesSub = append(dataValuesSub, b.GetPlaceholder())
					b.IOrm.SetBindValues(val.Interface())
				}
			} else if operType == "update" {
				// update
				if val.Interface() == nil {
					dataObj = append(dataObj, key+"=null")
				} else {
					dataObj = append(dataObj, key+"="+b.GetPlaceholder())
					b.IOrm.SetBindValues(val.Interface())
				}
			}
		}
		if operType == "insert" {
			// insert
			dataValues = append(dataValues, "("+strings.Join(dataValuesSub, ",")+")")
		}
	default:
		//ref := reflect.Indirect(reflect.ValueOf(data))
		//switch ref.Kind() {
		//case reflect.Struct:
		//	structEngin.New().StructContent2Map(data)
		//case reflect.Map:
		//case reflect.Slice:
		//	switch ref.Type().Elem().Kind() {
		//	case reflect.Struct:
		//	case reflect.Map:
		//	}
		//}

		return "", "", ""
	}

	return strings.Join(dataObj, ","), strings.Join(dataFields, ","), strings.Join(dataValues, ",")
}

func (b *BuilderDefault) parseStringData(operType string, data []string) (string, string, string) {
	if operType == "update" {
		return strings.Join(data, ","), "", ""
	}
	var dataFields []string
	var dataValues []string
	for _, s := range data {
		items := strings.Split(s, ",")
		for _, item := range items {
			fieldPairs := strings.Split(item, "=")
			if len(fieldPairs) <= 1 {
				continue
			}
			fieldName := fieldPairs[0]
			fieldValue := fieldPairs[1]
			dataFields = append(dataFields, fieldName)
			dataValues = append(dataValues, fieldValue)
		}
	}
	return "", strings.Join(dataFields, ","), strings.Join(dataValues, ",")
}

func (b *BuilderDefault) parseData(operType string, data []map[string]interface{}) (string, string, string) {
	// insert
	var dataFields []string
	var dataValues []string
	// update or delete
	var dataObj []string

	for key, _ := range data[0] {
		if inArray(key, dataFields) == false {
			dataFields = append(dataFields, key)
		}
	}
	for _, item := range data {
		// 定义1条数据的存储
		var dataValuesSub []string
		for _, key := range dataFields {
			if item[key] == nil {
				// 放入占位符
				dataValuesSub = append(dataValuesSub, b.GetPlaceholder())
				// 保存真正的值为null
				b.IOrm.SetBindValues("null")
			} else {
				// 放入占位符
				dataValuesSub = append(dataValuesSub, b.GetPlaceholder())
				// 保存真正的值
				b.IOrm.SetBindValues(item[key])
			}
			// update
			dataObj = append(dataObj, fmt.Sprintf("%s=%s", key, b.GetPlaceholder()))
		}
		dataValues = append(dataValues, "("+strings.Join(dataValuesSub, ",")+")")
	}
	return strings.Join(dataObj, ","), strings.Join(dataFields, ","), strings.Join(dataValues, ",")
}

func (b *BuilderDefault) BuildJoin() (s string, err error) {
	// 用户传入的join参数+join类型
	var join []interface{}
	var returnJoinArr []string
	joinArr := b.GetJoin()

	for _, join = range joinArr {
		var w string
		var ok bool
		// 用户传入 join 的where值, 即第二个参数
		var args []interface{}

		if len(join) != 2 {
			err = errors.New("join conditions are wrong")
			b.IOrm.GetISession().GetIEngin().GetLogger().Error(err.Error())
			return
		}

		// 获取真正的用户传入的join参数
		if args, ok = join[1].([]interface{}); !ok {
			err = errors.New("join conditions are wrong")
			b.IOrm.GetISession().GetIEngin().GetLogger().Error(err.Error())
			return
		}

		argsLength := len(args)
		var prefix = b.IOrm.GetISession().GetIEngin().GetPrefix()
		switch argsLength {
		case 1: // join字符串 raw
			//w = args[0].(string)
			w = fmt.Sprintf("%s%s", prefix, args[0])
		case 2: // join表 + 字符串
			//w = args[0].(string) + " ON " + args[1].(string)
			w = fmt.Sprintf("%s%s ON %s", prefix, args[0], args[1])
		case 4: // join表 + (a字段+关系+a字段)
			//w = args[0].(string) + " ON " + args[1].(string) + " " + args[2].(string) + " " + args[3].(string)

			w = fmt.Sprintf("%s%s ON %s %s %s", prefix, args[0], args[1], args[2], args[3])
		default:
			err = errors.New("join format error")
			b.IOrm.GetISession().GetIEngin().GetLogger().Error(err.Error())
			return
		}

		returnJoinArr = append(returnJoinArr, " "+join[0].(string)+" JOIN "+w)
	}

	return strings.Join(returnJoinArr, " "), nil
}

func (b *BuilderDefault) BuildWhere() (where string, err error) {
	var beforeParseWhere = b.IOrm.GetWhere()
	where, err = b.parseWhere(b.IOrm)
	b.IOrm.SetWhere(beforeParseWhere)
	return If(where == "", "", " WHERE "+where).(string), err
}

func (b *BuilderDefault) BuildDistinct() (dis string) {
	return If(b.IOrm.GetDistinct(), "DISTINCT ", "").(string)
}

func (b *BuilderDefault) BuildFields() string {
	if len(b.IOrm.GetFields()) == 0 {
		return "*"
	}
	return strings.Join(b.IOrm.GetFields(), ",")
}

func (b *BuilderDefault) BuildTable() string {
	return b.IOrm.GetTable()
}

func (b *BuilderDefault) BuildGroup() string {
	return If(b.IOrm.GetGroup() == "", "", " GROUP BY "+b.IOrm.GetGroup()).(string)
}

func (b *BuilderDefault) BuildHaving() string {
	return If(b.IOrm.GetHaving() == "", "", " HAVING "+b.IOrm.GetHaving()).(string)
}

func (b *BuilderDefault) BuildOrder() string {
	return If(b.IOrm.GetOrder() == "", "", " ORDER BY "+b.IOrm.GetOrder()).(string)
}

func (b *BuilderDefault) BuildLimit() string {
	if b.IOrm.GetUnion() != nil {
		return ""
	}
	return If(b.IOrm.GetLimit() == 0, "", " LIMIT "+strconv.Itoa(b.IOrm.GetLimit())).(string)
}

func (b *BuilderDefault) BuildOffset() string {
	if b.BuildLimit() == "" {
		return ""
	}
	if b.IOrm.GetUnion() != nil {
		return ""
	}
	return If(b.IOrm.GetOffset() == 0, "", " OFFSET "+strconv.Itoa(b.IOrm.GetOffset())).(string)
}

func (b *BuilderDefault) parseWhereInParams(value []interface{}) string {
	var placeHolder []string
	for _, v := range value {
		placeHolder = append(placeHolder, b.GetPlaceholder())
		b.IOrm.SetBindValues(v)
	}
	return strings.Join(placeHolder, ",")
}

func (b *BuilderDefault) parseSliceWhere(condition string, wheres []interface{}) (string, error) {
	// 		[
	// 			["age", ">", 1],
	// 			["money": [">", 10]],
	// 			["$or":
	// 				[
	// 					["name": "3232"],
	// 					["name": "like", "sss"]
	// 				]
	// 			],
	//			["$string": "status = 1"],
	//			["status = 1"],
	//			["type": ["$in": [2,3]]]
	//			["classid": ["$not_in": [12,33] ]]
	// 		]
	fmt.Printf("******========>>>%v\n",wheres)
	var results []string
	for _, args := range wheres {
		switch value := args.(type) {
		case string:
			results = append(results, value)
			break
		case []interface{}:
			fmt.Printf("==========>%v", value)
			fieldOrCondition := value[0].(string)
			valueLen := len(value)

			if valueLen == 1 {
				// 只有一个参数
				results = append(results, fieldOrCondition)
				continue
			}
			if valueLen == 2 {
				paramValue := value[1]
				paramValueType := reflect.TypeOf(paramValue).Kind()

				switch strings.ToLower(fieldOrCondition) {
				case "and", "$and":
					if paramValueType != reflect.Slice {
						return "", errors.New("查询参数无效")
					}
					tmpWhere, err := b.parseSliceWhere("and", paramValue.([]interface{}))
					if err != nil {
						return "", err
					}
					if tmpWhere != "" {
						results = append(results, tmpWhere)
					}
					break
				case "or", "$or":
					if paramValueType != reflect.Slice {
						return "", errors.New("查询参数无效")
					}
					tmpWhere, err := b.parseSliceWhere("or", paramValue.([]interface{}))
					if err != nil {
						return "", err
					}
					if tmpWhere != "" {
						results = append(results, tmpWhere)
					}
					break
				case "$string":
					if paramValueType != reflect.String {
						return "", errors.New("查询参数无效")
					}
					results = append(results, paramValue.(string))
					break
				default:
					if paramValueType == reflect.Slice {
						arr := paramValue.([]interface{})
						if oper, ok := arr[0].(string); !ok || len(arr) == 0 {
							continue
						} else {
							results = append(results, fmt.Sprintf("%s %s %s", fieldOrCondition, oper, b.GetPlaceholder()))
							b.SetBindValues(arr[1])
						}
					} else {
						results = append(results, fieldOrCondition + " = " + b.GetPlaceholder())
						b.SetBindValues(paramValue)
					}
				}
			}
			// ["name", "like", "sss"]
			if valueLen == 3 {
				oper := value[1].(string)
				switch oper {
				case "like", "$like":
					results = append(results, fieldOrCondition+" like " + b.GetPlaceholder())
					b.IOrm.SetBindValues(value[2])
					continue
				case ">", ">=", "<>", "<", "<=":
					results = append(results, fieldOrCondition+oper + b.GetPlaceholder())
					b.IOrm.SetBindValues(value[2])
					continue
				case "in", "$in", "not in", "$not_in":
					oper1 := oper
					if oper == "$in" {
						oper1 = "in"
					} else if oper == "$not_in" {
						oper1 = "not in"
					}
					if arr, ok := value[2].([]interface{}); !ok {
						return "", errors.New("查询参数无效")
					} else {
						s := b.parseWhereInParams(arr)
						if s != "" {
							results = append(results, fmt.Sprintf("%s %s %s", fieldOrCondition, oper1, s))
						}
					}
					break
				case "between", "not between":
					if arr, ok := value[2].([]interface{}); !ok {
						return "", errors.New("查询参数无效")
					} else {
						if len(arr) > 0 {
							if len(arr) == 1 {
								b.SetBindValues(arr[0])
								b.SetBindValues(arr[0])
							} else if len(arr) > 1 {
								b.SetBindValues(arr[0])
								b.SetBindValues(arr[1])
							}
							results = append(results, fieldOrCondition + " " + oper + " " + b.GetPlaceholder() + " and " + b.GetPlaceholder())
						}
					}
					break
				}
			}
		}
	}
	return strings.Join(results, condition), nil
}

func (b *BuilderDefault) parseMapWhere(condition string, wheres map[string]interface{}) (string, error) {
	return "", nil
}

// parseWhere : parse where condition
func (b *BuilderDefault) parseWhere(ormApi IOrm) (string, error) {
	// 取出所有where
	wheres := ormApi.GetWhere()
	where, vars := sqlw.BuildWhereSql(b.GetPlaceholder(), wheres)
	if where == "" {
		return "", nil
	}
	b.SetBindValues(vars...)
	return where, nil
}

/**
 * 将where条件中的参数转换为where条件字符串
 * example: {"id",">",1}, {"age", 18}
 */
// parseParams : 将where条件中的参数转换为where条件字符串
func (b *BuilderDefault) parseParams(args []interface{}, ormApi IOrm) (s string, err error) {
	paramsLength := len(args)
	argsReal := args

	// 存储当前所有数据的数组
	var paramsToArr []string

	switch paramsLength {
	case 3: // 常规3个参数:  {"id",">",1}
		//if !inArray(argsReal[1], b.GetRegex()) {
		if !inArray(argsReal[1], b.GetOperator()) {
			err = errors.New("where parameter is wrong")
			b.IOrm.GetISession().GetIEngin().GetLogger().Error(err.Error())
			return
		}

		paramsToArr = append(paramsToArr, argsReal[0].(string))
		paramsToArr = append(paramsToArr, argsReal[1].(string))

		switch strings.Trim(strings.ToLower(t.New(argsReal[1]).String()), " ") {
		//case "like", "not like":
		//	paramsToArr = append(paramsToArr, b.GetPlaceholder())
		//	b.IOrm.SetBindValues(argsReal[2])
		case "in", "not in":
			var tmp []string
			var ar2 = t.New(argsReal[2]).Slice()
			for _, item := range ar2 {
				tmp = append(tmp, b.GetPlaceholder())
				b.IOrm.SetBindValues(t.New(item).Interface())
			}
			paramsToArr = append(paramsToArr, "("+strings.Join(tmp, ",")+")")

		case "between", "not between":
			var ar2 = t.New(argsReal[2]).Slice()
			paramsToArr = append(paramsToArr, b.GetPlaceholder()+" and "+b.GetPlaceholder())
			b.IOrm.SetBindValues(ar2[0].Interface())
			b.IOrm.SetBindValues(ar2[1].Interface())

		default:
			paramsToArr = append(paramsToArr, b.GetPlaceholder())
			b.IOrm.SetBindValues(argsReal[2])
		}
	case 2:
		paramsToArr = append(paramsToArr, argsReal[0].(string))
		paramsToArr = append(paramsToArr, "=")
		paramsToArr = append(paramsToArr, b.GetPlaceholder())
		b.IOrm.SetBindValues(argsReal[1])
	}

	return strings.Join(paramsToArr, " "), nil
}

func (b *BuilderDefault) GetOperator() []string {
	return b.operator
}
