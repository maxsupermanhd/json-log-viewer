package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/a-h/templ"
	"github.com/davecgh/go-spew/spew"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	log.Info().Msg("hello world")

	mux := http.NewServeMux()
	mux.HandleFunc("/", http.NotFound)
	mux.HandleFunc("/{$}", handleIndex)
	mux.HandleFunc("/view/{dirName}", handleLogDir)
	mux.HandleFunc("/view/{dirName}/{ruleSetName}", handleLogDir)
	mux.Handle("/static/style.css", triviaFileServer{fp: "static/style.css"})
	mux.Handle("/static/charts.min.css", triviaFileServer{fp: "static/charts.min.css"})

	listenAddr := ":9172"
	log.Info().Str("addr", listenAddr).Msg("listening")
	log.Err(http.ListenAndServe(listenAddr, mux)).Msg("handle")
}

type Rule struct {
	Op   string
	Data any
}

func (r Rule) Run(rules ruleset, arg any) (bool, error) {
	op, ok := rules[r.Op]
	if !ok {
		return false, fmt.Errorf("run rule op %q not found", r.Op)
	}
	return op(rules, r.Data, arg)
}

func ruleDataToRule(data any) (ret Rule, err error) {
	obj, ok := data.(map[string]any)
	if !ok {
		return ret, fmt.Errorf("data to rule: %q not an object", data)
	}
	ret.Op, ok = obj["Op"].(string)
	if !ok {
		return ret, fmt.Errorf("data to rule: Op %q not a string", obj["Op"])
	}
	ret.Data = obj["Data"]
	return ret, nil
}

type ruleOpFn func(rules ruleset, data, arg any) (bool, error)

type ruleset map[string]ruleOpFn

var (
	definedRuleOps = ruleset{
		"not": func(rules ruleset, data, arg any) (bool, error) {
			d, err := ruleDataToRule(data)
			if err != nil {
				return false, fmt.Errorf("rule not: data is not rule: %w", err)
			}
			ret, err := d.Run(rules, arg)
			return !ret, err
		},
		"or": func(rules ruleset, data, arg any) (bool, error) {
			els, ok := data.([]any)
			if !ok {
				return false, fmt.Errorf("rule or: data is not array (%q)", spew.Sdump(data))
			}
			for i, el := range els {
				d, err := ruleDataToRule(el)
				if err != nil {
					return false, fmt.Errorf("rule or: data %d is not rule: %w", i, err)
				}
				ret, err := d.Run(rules, arg)
				if err != nil {
					return ret, fmt.Errorf("running or rule %d: %w", i, err)
				}
				if ret {
					return true, nil
				}
			}
			return false, nil
		},
		"and": func(rules ruleset, data, arg any) (bool, error) {
			els, ok := data.([]any)
			if !ok {
				return false, fmt.Errorf("rule and: data is not array (%q)", spew.Sdump(data))
			}
			for i, el := range els {
				d, err := ruleDataToRule(el)
				if err != nil {
					return false, fmt.Errorf("rule and: data %d is not rule: %w", i, err)
				}
				ret, err := d.Run(rules, arg)
				if err != nil {
					return ret, fmt.Errorf("running and rule %d: %w", i, err)
				}
				if !ret {
					return false, nil
				}
			}
			return true, nil
		},
		"contains": func(rules ruleset, data, arg any) (bool, error) {
			d, ok := arg.(string)
			if !ok {
				return false, errors.New("rule contains: arg is not string")
			}
			check, ok := data.(string)
			if !ok {
				return false, errors.New("rule contains: data is not string")
			}
			return strings.Contains(d, check), nil
		},
	}
)

type SavedStuff struct {
	RuleSets map[string]*Rule
	LogDirs  map[string]map[string]*Rule
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	saved := SavedStuff{}
	must(json.NewDecoder(bytes.NewReader(noerr(os.ReadFile("saved.json")))).Decode(&saved))
	templ.Handler(tPage(tIndex(saved))).ServeHTTP(w, r)
}

func handleLogDir(w http.ResponseWriter, r *http.Request) {
	savedBytes, err := os.ReadFile("saved.json")
	if err != nil {
		templ.Handler(tPage(tMessage(err.Error()))).ServeHTTP(w, r)
		return
	}
	saved := SavedStuff{}
	err = json.Unmarshal(savedBytes, &saved)
	if err != nil {
		templ.Handler(tPage(tMessage(err.Error()))).ServeHTTP(w, r)
		return
	}
	dirName := r.PathValue("dirName")
	ruleSetName := r.PathValue("ruleSetName")
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		limit = 500
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil {
		offset = 0
	}
	step, err := strconv.Atoi(r.URL.Query().Get("step"))
	if err != nil {
		step = 500
	}

	var rule *Rule
	dirRules, ok := saved.LogDirs[dirName]
	if ok {
		rule = dirRules[ruleSetName]
	}
	if rule == nil {
		rule = saved.RuleSets[ruleSetName]
	}

	messages, err := processDir(dirName, rule, limit, offset)
	if err != nil {
		templ.Handler(tPage(tMessage(err.Error()))).ServeHTTP(w, r)
		return
	}

	templ.Handler(tPage(tView(dirName, ruleSetName, slices.Sorted(maps.Keys(saved.RuleSets)), slices.Sorted(maps.Keys(dirRules)), limit, offset, step, messages))).ServeHTTP(w, r)
}

func processDir(dirPath string, rule *Rule, limit, offset int) ([]map[string]any, error) {
	d, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}
	buf := NewLogBuffer(limit + offset)
	for _, de := range d {
		if de.IsDir() {
			continue
		}
		n := de.Name()
		if !strings.HasSuffix(n, ".log") {
			continue
		}
		f, err := os.Open(filepath.Join(dirPath, de.Name()))
		if err != nil {
			return nil, err
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		if rule == nil {
			for scanner.Scan() {
				buf.Push(scanner.Text())
			}
		} else {
			for scanner.Scan() {
				match, err := rule.Run(definedRuleOps, scanner.Text())
				if err != nil {
					return nil, fmt.Errorf("processing rule on line %q: %w", scanner.Text(), err)
				}
				if match {
					buf.Push(scanner.Text())
				}
			}
		}
	}
	ret := []map[string]any{}
	msgs, err := buf.Get(offset, limit)
	if err != nil {
		return nil, err
	}
	for _, msg := range slices.Backward(msgs) {
		msgParsed := map[string]any{}
		err = json.Unmarshal([]byte(msg), &msgParsed)
		if err != nil {
			ret = append(ret, map[string]any{"message": msg})
			continue
		}
		ret = append(ret, msgParsed)
	}
	return ret, nil
}

func marshalOtherParams(msg map[string]any) (ret string) {
	skip := []string{"level", "time", "message"}
	for _, k := range slices.Sorted(maps.Keys(msg)) {
		if slices.Contains(skip, k) {
			continue
		}
		ret += fmt.Sprintf("%q=%v ", k, msg[k])
	}
	return ret
}

type triviaFileServer struct {
	fp string
}

func (s triviaFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, s.fp)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func noerr[T any](ret T, err error) T {
	must(err)
	return ret
}
