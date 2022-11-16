/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package channelconfig

import (
	"fmt"
	"reflect"

	"github.com/golang/protobuf/proto"
	cb "github.com/hyperledger/fabric-protos-go/common"
)

// DeserializeGroup deserializes the value for all values in a config group
// 反序列化config group所有的值
func DeserializeProtoValuesFromGroup(group *cb.ConfigGroup, protosStructs ...interface{}) error {
	// 将结构体转换成 map[string]*proto.Message
	sv, err := NewStandardValues(protosStructs...)
	if err != nil {
		logger.Panicf("This is a compile time bug only, the proto structures are somehow invalid: %s", err)
	}

	for key, value := range group.Values {
		if _, err := sv.Deserialize(key, value.Value); err != nil {
			return err
		}
	}
	return nil
}

type StandardValues struct {
	lookup map[string]proto.Message
}

// NewStandardValues accepts a structure which must contain only protobuf message
// types.  The structure may embed other (non-pointer) structures which satisfy
// the same condition.  NewStandard values will instantiate memory for all the proto
// messages and build a lookup map from structure field name to proto message instance
// This is a useful way to easily implement the Values interface

// 接受一个结构体，必须只包含protobuf消息类型， 这个结构可以嵌入非指针真的结构的满足相同条件的值，
// NewStandard值将为所有的原型实例化内存，，并构建一个从结构字段名到proto消息实例的查找映射
//这是一个很容易实现Values接口的有用方法
func NewStandardValues(protosStructs ...interface{}) (*StandardValues, error) {
	sv := &StandardValues{
		lookup: make(map[string]proto.Message),
	}

	for _, protosStruct := range protosStructs {
		logger.Debugf("Initializing protos for %T\n", protosStruct)
		if err := sv.initializeProtosStruct(reflect.ValueOf(protosStruct)); err != nil {
			return nil, err
		}
	}

	return sv, nil
}

// Deserialize looks up the backing Values proto of the given name, unmarshals the given bytes
// to populate the backing message structure, and returns a referenced to the retained deserialized
// message (or an error, either because the key did not exist, or there was an an error unmarshalling
func (sv *StandardValues) Deserialize(key string, value []byte) (proto.Message, error) {
	msg, ok := sv.lookup[key]
	// 首先查看键是否存在
	if !ok {
		return nil, fmt.Errorf("Unexpected key %s", key)
	}
	// 将结构体中的value解码到*Proto.Message中
	err := proto.Unmarshal(value, msg)
	if err != nil {
		return nil, err
	}

	return msg, nil
}

// 将结构体中的元素名称和类型转换为 map[string]*proto.Message
func (sv *StandardValues) initializeProtosStruct(objValue reflect.Value) error {
	objType := objValue.Type()
	// 如果不是是指针类型 ， 直接返回错误
	if objType.Kind() != reflect.Ptr {
		return fmt.Errorf("Non pointer type")
	}
	// 如果元素类型不是结构体，返回错误
	if objType.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("Non struct type")
	}

	numFields := objValue.Elem().NumField() // 结构体元素数量
	for i := 0; i < numFields; i++ {
		structField := objType.Elem().Field(i) // 获取到结构体字段
		logger.Debugf("Processing field: %s\n", structField.Name)
		switch structField.Type.Kind() {
		// 如果元素类型是结构体类型
		case reflect.Ptr:
			fieldPtr := objValue.Elem().Field(i)
			if !fieldPtr.CanSet() {
				return fmt.Errorf("Cannot set structure field %s (unexported?)", structField.Name)
			}
			fieldPtr.Set(reflect.New(structField.Type.Elem()))
		default:
			return fmt.Errorf("Bad type supplied: %s", structField.Type.Kind())
		}

		proto, ok := objValue.Elem().Field(i).Interface().(proto.Message)
		if !ok {
			return fmt.Errorf("Field type %T does not implement proto.Message", objValue.Elem().Field(i))
		}

		_, ok = sv.lookup[structField.Name]
		if ok {
			return fmt.Errorf("Ambiguous field name specified, multiple occurrences of %s", structField.Name)
		}

		sv.lookup[structField.Name] = proto
	}

	return nil
}
