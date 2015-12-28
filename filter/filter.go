// Copyright 2015 Eleme Inc. All rights reserved.

// Package filter implements fast wildcard like filtering based on suffix
// tree.
package filter

import (
	"strings"
	"sync"

	"github.com/eleme/banshee/models"
	"github.com/eleme/banshee/storage"
	"github.com/eleme/banshee/util/safemap"
)

// Filter is to filter metrics by rules.
type Filter struct {
	// Rule changes
	addRuleCh chan *models.Rule
	delRuleCh chan *models.Rule
	// Children
	children *safemap.SafeMap
}

// childFilter is a suffix tree.
type childFilter struct {
	lock         *sync.RWMutex
	matchedRules []*models.Rule
	children     *safemap.SafeMap
}

// Limit for buffered changed rules
const bufferedChangedRulesLimit = 1000

// New creates a filter.
func New() *Filter {
	return &Filter{
		addRuleCh: make(chan *models.Rule, bufferedChangedRulesLimit),
		delRuleCh: make(chan *models.Rule, bufferedChangedRulesLimit),
		children:  safemap.New(),
	}
}

// Init from db.
func (f *Filter) Init(db *storage.DB) {
	// Listen rules changes.
	db.Admin.RulesCache.OnAdd(f.addRuleCh)
	db.Admin.RulesCache.OnDel(f.delRuleCh)
	go f.addRules()
	go f.delRules()
	// Add rules from cache
	var rules []*models.Rule
	db.Admin.RulesCache.All(&rules)
	for _, rule := range rules {
		f.addRule(rule)
	}
}

// newChildCache creates a new childCache
func newChildFilter() *childFilter {
	return &childFilter{
		lock:         &sync.RWMutex{},
		matchedRules: []*models.Rule{},
		children:     nil,
	}
}

func (c *childFilter) matchedRs(l []string) []*models.Rule {
	if len(l) == 0 {
		c.lock.RLock()
		defer c.lock.RUnlock()
		return c.matchedRules
	}
	rules := []*models.Rule{}
	if c.children == nil {
		return rules
	}
	v, exist := c.children.Get("*")
	if exist {
		rules = append(rules, v.(*childFilter).matchedRs(l[1:])...)
	}
	v, exist = c.children.Get(l[0])
	if exist {
		rules = append(rules, v.(*childFilter).matchedRs(l[1:])...)
	}
	return rules
}

// MatchedRules checks if a metric hit the hitCache, if hit return all hit rules
func (f *Filter) MatchedRules(m *models.Metric) []*models.Rule {
	rules := []*models.Rule{}
	l := strings.Split(m.Name, ".")
	v, exist := f.children.Get("*")
	if exist {
		rules = append(rules, v.(*childFilter).matchedRs(l[1:])...)
	}
	v, exist = f.children.Get(l[0])
	if exist {
		rules = append(rules, v.(*childFilter).matchedRs(l[1:])...)
	}
	return rules
}

// addRule adds a new rule to the filter.
func (f *Filter) addRule(rule *models.Rule) {
	l := strings.Split(rule.Pattern, ".")
	if !f.children.Has(l[0]) {
		f.children.Set(l[0], newChildFilter())
	}
	v, _ := f.children.Get(l[0])
	l = l[1:]
	for len(l) > 0 {
		if v.(*childFilter).children == nil {
			v.(*childFilter).children = safemap.New()
		}
		if v.(*childFilter).children.Has(l[0]) {
			v, _ = v.(*childFilter).children.Get(l[0])
		} else {
			v.(*childFilter).children.Set(l[0], newChildFilter())
			v, _ = v.(*childFilter).children.Get(l[0])
		}
		l = l[1:]
	}
	v.(*childFilter).lock.Lock()
	defer v.(*childFilter).lock.Unlock()
	v.(*childFilter).matchedRules = append(v.(*childFilter).matchedRules, rule)
}

// delRule deletes a rule from the filter.
func (f *Filter) delRule(rule *models.Rule) {
	l := strings.Split(rule.Pattern, ".")
	if !f.children.Has(l[0]) {
		return
	}
	v, _ := f.children.Get(l[0])
	l = l[1:]
	for len(l) > 0 {
		if v.(*childFilter).children == nil {
			return
		}
		if v.(*childFilter).children.Has(l[0]) {
			v, _ = v.(*childFilter).children.Get(l[0])
		} else {
			return
		}
		l = l[1:]
	}
	v.(*childFilter).lock.Lock()
	defer v.(*childFilter).lock.Unlock()
	rules := []*models.Rule{}
	for _, r := range v.(*childFilter).matchedRules {
		if !rule.Equal(r) {
			rules = append(rules, r)
		}
	}
	v.(*childFilter).matchedRules = rules
}

// addRules waits and add new rule to filter.
func (f *Filter) addRules() {
	for {
		rule := <-f.addRuleCh
		f.addRule(rule)
	}
}

// delRules waits and delete rule from filter.
func (f *Filter) delRules() {
	for {
		rule := <-f.delRuleCh
		f.delRule(rule)
	}
}