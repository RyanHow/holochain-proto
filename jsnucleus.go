// Copyright (C) 2013-2017, The MetaCurrency Project (Eric Harris-Braun, Arthur Brock, et. al.)
// Use of this source code is governed by GPLv3 found in the LICENSE file
//----------------------------------------------------------------------------------------
// JSNucleus implements a javascript use of the Nucleus interface

package holochain

import (
	"errors"
	"fmt"
	peer "github.com/libp2p/go-libp2p-peer"
	"github.com/robertkrimen/otto"
	"strings"
	"time"
)

const (
	JSNucleusType = "js"
)

type JSNucleus struct {
	vm         *otto.Otto
	interfaces []Interface
	lastResult *otto.Value
}

// Type returns the string value under which this nucleus is registered
func (z *JSNucleus) Type() string { return JSNucleusType }

// ChainGenesis runs the application genesis function
// this function gets called after the genesis entries are added to the chain
func (z *JSNucleus) ChainGenesis() (err error) {
	v, err := z.vm.Run(`genesis()`)
	if err != nil {
		err = fmt.Errorf("Error executing genesis: %v", err)
		return
	}
	if v.IsBoolean() {
		var b bool
		b, err = v.ToBoolean()
		if err != nil {
			return
		}
		if !b {
			err = fmt.Errorf("genesis failed")
		}

	} else {
		err = fmt.Errorf("genesis should return boolean, got: %v", v)
	}
	return
}

// ValidateCommit checks the contents of an entry against the validation rules at commit time
func (z *JSNucleus) ValidateCommit(d *EntryDef, entry Entry, header *Header, sources []string) (err error) {
	err = z.validateEntry("validateCommit", d, entry, header, sources)
	return
}

// ValidatePut checks the contents of an entry against the validation rules at DHT put time
func (z *JSNucleus) ValidatePut(d *EntryDef, entry Entry, header *Header, sources []string) (err error) {
	err = z.validateEntry("validatePut", d, entry, header, sources)
	return
}

// ValidatePutMeta checks the putmeta data against the validation rules at putmeta
func (z *JSNucleus) ValidatePutMeta(baseType string, baseHash string, ptrType, ptrHash string, tag string, sources []string) (err error) {
	srcs := mkJSSources(sources)
	code := fmt.Sprintf(`validatePutMeta("%s","%s","%s","%s","%s",%s)`, baseType, baseHash, ptrType, ptrHash, tag, srcs)
	Debugf("validatePutMeta: %s", code)

	err = z.runValidate("validatePutMeta", code)
	return
}

func mkJSSources(sources []string) (srcs string) {
	srcs = `["` + strings.Join(sources, `","`) + `"]`
	return
}

func (z *JSNucleus) prepareValidateArgs(d *EntryDef, entry Entry, sources []string) (e string, srcs string, err error) {
	c := entry.Content().(string)
	switch d.DataFormat {
	case DataFormatRawJS:
		e = c
	case DataFormatString:
		e = "\"" + jsSanitizeString(c) + "\""
	case DataFormatJSON:
		e = fmt.Sprintf(`JSON.parse("%s")`, jsSanitizeString(c))
	default:
		err = errors.New("data format not implemented: " + d.DataFormat)
		return
	}
	srcs = mkJSSources(sources)
	return
}

func (z *JSNucleus) runValidate(fnName string, code string) (err error) {
	var v otto.Value
	v, err = z.vm.Run(code)
	if err != nil {
		err = fmt.Errorf("Error executing %s: %v", fnName, err)
		return
	}
	if v.IsBoolean() {
		if v.IsBoolean() {
			var b bool
			b, err = v.ToBoolean()
			if err != nil {
				return
			}
			if !b {
				err = ValidationFailedErr
			}
		}
	} else {
		err = fmt.Errorf("%s should return boolean, got: %v", fnName, v)
	}
	return
}

func (z *JSNucleus) validateEntry(fnName string, d *EntryDef, entry Entry, header *Header, sources []string) (err error) {

	e, srcs, err := z.prepareValidateArgs(d, entry, sources)
	if err != nil {
		return
	}

	hdr := fmt.Sprintf(
		`{"EntryLink":"%s","Type":"%s","Time":"%s"}`,
		header.EntryLink.String(),
		header.Type,
		header.Time.UTC().Format(time.RFC3339),
	)

	code := fmt.Sprintf(`%s("%s",%s,%s,%s)`, fnName, d.Name, e, hdr, srcs)
	Debugf("%s: %s", fnName, code)
	err = z.runValidate(fnName, code)
	if err != nil && err == ValidationFailedErr {
		err = fmt.Errorf("Invalid entry: %v", entry.Content())
	}

	return
}

// GetInterface returns an Interface of the given name
func (z *JSNucleus) GetInterface(iface string) (i *Interface, err error) {
	for _, x := range z.interfaces {
		if x.Name == iface {
			i = &x
			break
		}
	}
	if i == nil {
		err = errors.New("couldn't find exposed function: " + iface)
	}
	return
}

// Interfaces returns the list of application exposed functions the nucleus
func (z *JSNucleus) Interfaces() (i []Interface) {
	i = z.interfaces
	return
}

// expose registers an interfaces defined in the DNA for calling by external clients
// (you should probably never need to call this function as it is called by the DNA's expose functions)
func (z *JSNucleus) expose(iface Interface) (err error) {
	z.interfaces = append(z.interfaces, iface)
	return
}

const (
	JSLibrary = `var HC={STRING:0,JSON:1,Version:` + `"` + VersionStr + `"};`
)

// jsSanatizeString makes sure all quotes are quoted and returns are removed
func jsSanitizeString(s string) string {
	s0 := strings.Replace(s, "\n", "", -1)
	s1 := strings.Replace(s0, "\r", "", -1)
	s2 := strings.Replace(s1, "\"", "\\\"", -1)
	return s2
}

// Call calls the zygo function that was registered with expose
func (z *JSNucleus) Call(iface string, params interface{}) (result interface{}, err error) {
	var i *Interface
	i, err = z.GetInterface(iface)
	if err != nil {
		return
	}
	var code string
	switch i.Schema {
	case STRING:
		code = fmt.Sprintf(`%s("%s");`, iface, jsSanitizeString(params.(string)))
	case JSON:
		if params.(string) == "" {
			code = fmt.Sprintf(`JSON.stringify(%s());`, iface)
		} else {
			p := jsSanitizeString(params.(string))
			code = fmt.Sprintf(`JSON.stringify(%s(JSON.parse("%s")));`, iface, p)
		}
	default:
		err = errors.New("params type not implemented")
		return
	}
	Debugf("JS Call: %s", code)
	var v otto.Value
	v, err = z.vm.Run(code)
	if err == nil {
		if v.IsObject() && v.Class() == "Error" {
			Debugf("JS Error:\n%v", v)
			var message otto.Value
			message, err = v.Object().Get("message")
			if err == nil {
				err = errors.New(message.String())
			}
		} else {
			result, err = v.ToString()
		}
	}
	return
}

// NewJSNucleus builds a javascript execution environment with user specified code
func NewJSNucleus(h *Holochain, code string) (n Nucleus, err error) {
	var z JSNucleus
	z.vm = otto.New()

	err = z.vm.Set("property", func(call otto.FunctionCall) otto.Value {
		prop, _ := call.Argument(0).ToString()

		p, err := h.GetProperty(prop)
		if err != nil {
			return otto.UndefinedValue()
		}
		result, _ := z.vm.ToValue(p)
		return result
	})
	if err != nil {
		return nil, err
	}

	err = z.vm.Set("debug", func(call otto.FunctionCall) otto.Value {
		msg, _ := call.Argument(0).ToString()
		h.config.Loggers.App.p(msg)
		return otto.UndefinedValue()
	})

	err = z.vm.Set("expose", func(call otto.FunctionCall) otto.Value {
		fnName, _ := call.Argument(0).ToString()
		schema, _ := call.Argument(1).ToInteger()
		i := Interface{Name: fnName, Schema: InterfaceSchemaType(schema)}
		err = z.expose(i)
		if err != nil {
			return z.vm.MakeCustomError("HolochainError", err.Error())
		}
		return otto.UndefinedValue()
	})
	if err != nil {
		return nil, err
	}

	err = z.vm.Set("commit", func(call otto.FunctionCall) otto.Value {
		entryType, _ := call.Argument(0).ToString()
		var entry string
		v := call.Argument(1)

		if v.IsString() {
			entry, _ = v.ToString()
		} else if v.IsObject() {
			v, _ = z.vm.Call("JSON.stringify", nil, v)
			entry, _ = v.ToString()
		} else {
			return z.vm.MakeCustomError("HolochainError", "commit expected string as second argument")
		}

		e := GobEntry{C: entry}
		var l int
		var hash Hash
		var header *Header
		l, hash, header, err = h.chain.PrepareHeader(h.hashSpec, time.Now(), entryType, &e, h.agent.PrivKey())
		if err != nil {
			return z.vm.MakeCustomError("HolochainError", err.Error())
		}

		err = h.ValidateCommit(entryType, &e, header, []peer.ID{h.id})

		if err == nil {
			err = h.chain.addEntry(l, hash, header, &e)
		}
		if err != nil {
			return z.vm.MakeCustomError("HolochainError", err.Error())
		}

		result, _ := z.vm.ToValue(header.EntryLink.String())
		return result
	})
	if err != nil {
		return nil, err
	}

	err = z.vm.Set("put", func(call otto.FunctionCall) otto.Value {
		v := call.Argument(0)
		var hashstr string

		if v.IsString() {
			hashstr, _ = v.ToString()
		} else {
			return z.vm.MakeCustomError("HolochainError", "put expected string as argument")
		}

		var key Hash
		key, err = NewHash(hashstr)
		if err == nil {
			err = h.dht.SendPut(key)
		}

		if err != nil {
			return z.vm.MakeCustomError("HolochainError", err.Error())
		}

		return otto.UndefinedValue()
	})
	if err != nil {
		return nil, err
	}

	err = z.vm.Set("get", func(call otto.FunctionCall) (result otto.Value) {
		v := call.Argument(0)
		var hashstr string

		if v.IsString() {
			hashstr, _ = v.ToString()
		} else {
			return z.vm.MakeCustomError("HolochainError", "get expected string as argument")
		}

		var key Hash
		key, err = NewHash(hashstr)
		if err == nil {
			var response interface{}
			response, err = h.dht.SendGet(key)
			if err == nil {
				switch t := response.(type) {
				case *GobEntry:
					result, err = z.vm.ToValue(t)
					return
					// @TODO what about if the hash was of a header??
				default:
					err = fmt.Errorf("unexpected response type from SendGet: %v", t)
				}

			}
		}

		if err != nil {
			result = z.vm.MakeCustomError("HolochainError", err.Error())
			return
		}
		panic("Shouldn't get here!")
	})
	if err != nil {
		return nil, err
	}

	err = z.vm.Set("putmeta", func(call otto.FunctionCall) otto.Value {
		base, _ := call.Argument(0).ToString()
		metaHash, _ := call.Argument(1).ToString()
		tag, _ := call.Argument(2).ToString()

		err = h.PutMeta(base, metaHash, tag)
		if err != nil {
			return z.vm.MakeCustomError("HolochainError", err.Error())
		}

		return otto.UndefinedValue()
	})
	if err != nil {
		return nil, err
	}

	err = z.vm.Set("getmeta", func(call otto.FunctionCall) (result otto.Value) {
		base, _ := call.Argument(0).ToString()
		tag, _ := call.Argument(1).ToString()

		var response interface{}
		response, err = h.GetMeta(base, tag)
		if err == nil {
			result, err = z.vm.ToValue(response)
		} else {
			return z.vm.MakeCustomError("HolochainError", err.Error())
		}

		return
	})
	if err != nil {
		return nil, err
	}
	l := JSLibrary
	if h != nil {
		l += fmt.Sprintf(`var App = {Name:"%s",DNA:{Hash:"%s"},Agent:{Hash:"%s",String:"%s"},Key:{Hash:"%s"}};`, h.Name, h.dnaHash, h.agentHash, h.Agent().Name(), peer.IDB58Encode(h.id))
	}
	_, err = z.Run(l + code)
	if err != nil {
		return
	}
	n = &z
	return
}

// Run executes javascript code
func (z *JSNucleus) Run(code string) (result *otto.Value, err error) {
	v, err := z.vm.Run(code)
	if err != nil {
		err = errors.New("JS exec error: " + err.Error())
		return
	}
	z.lastResult = &v
	return
}
