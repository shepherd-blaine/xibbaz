package main

import (
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "strconv"
    "strings"
    "time"
)

type Alerter struct {
    alerts map[string]string
}

func NewAlerter() *Alerter {
    return &Alerter{
        alerts: map[string]string{
            "la":          "Load Average is too high: %d",
            "ram":         "Memory usage too high: %d%%",
            "disk":        "Free disk space is too low: %d Mb left",
            "net":         "Network bandwidth usage high: %d Mbit/s available",
            "fetch_error": "Unable to fetch server statistic",
            "_unknown":    "Unknown param alert: %d, param: %s",
        },
    }
}

func (a *Alerter) Alert(param string, value int) {
    if msg, exists := a.alerts[param]; exists {
        if param == "fetch_error" {
            fmt.Println(msg)
        } else {
            fmt.Printf(msg+"\n", value)
        }
    } else {
        fmt.Printf(a.alerts["_unknown"]+"\n", value, param)
    }
}

type ValueFormatter struct{}

func NewValueFormatter() *ValueFormatter {
    return &ValueFormatter{}
}

func (vf *ValueFormatter) Format(param string, data map[string]int, result int) int {
    switch param {
        case "disk":
            return vf.diskValue(data, result)
        case "net":
            return vf.netValue(data, result)
        default:
            return vf.defaultValue(data, result)
    }
}

func (vf *ValueFormatter) netValue(data map[string]int, value int) int {
    return (data["total"] - data["res"]) / (1000 * 1000)
}

func (vf *ValueFormatter) diskValue(data map[string]int, value int) int {
    return (data["total"] - data["res"]) / (1024 * 1024)
}

func (vf *ValueFormatter) defaultValue(data map[string]int, value int) int {
    return value
}

type Limit struct {
    Type  string `json:"type"`
    Value int    `json:"value"`
}

type ResourceChecker struct {
    currentStats map[string]map[string]int
    uri          string
    limits       map[string]Limit
    alerter      *Alerter
    formatter    *ValueFormatter
    fetchErrorsCount  int
    fetchErrorsBarrierAlert  int
    statsCapacity int
}

func NewResourceChecker(alerter *Alerter, formatter *ValueFormatter) *ResourceChecker {
    return &ResourceChecker{
        alerter:   alerter,
        formatter: formatter,
        fetchErrorsBarrierAlert: 3,
        statsCapacity: 7,
    }
}

func (rc *ResourceChecker) SetURI(uri string) *ResourceChecker {
    rc.uri = uri
    return rc
}

func (rc *ResourceChecker) SetLimits(jsonStr string) *ResourceChecker {
    err := json.Unmarshal([]byte(jsonStr), &rc.limits)
    if err != nil {
        log.Printf("Error parsing limits JSON: %v", err)
    }
    return rc
}

func (rc *ResourceChecker) HasFetchErrorAlerts() bool {
    return rc.fetchErrorsCount >= rc.fetchErrorsBarrierAlert
}

func (rc *ResourceChecker) LoadInfo() *ResourceChecker {
    if rc.uri == "" {
        log.Println("URI not set")
        rc.fetchErrorsCount++
        return rc
    }

    resp, err := http.Get(rc.uri)
    if err != nil {
        log.Printf("Error fetching data: %v", err)
        rc.fetchErrorsCount++
        return rc
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        log.Printf("Error reading response: %v", err)
        rc.fetchErrorsCount++
        return rc
    }

    values := strings.Split(strings.TrimSpace(string(body)), ",")
    
    if resp.StatusCode != 200 || len(values) != rc.statsCapacity {
        
        rc.fetchErrorsCount++
        
        if rc.HasFetchErrorAlerts() {
            rc.alerter.Alert("fetch_error", rc.fetchErrorsCount)
        }
        
        return rc
    }
    
    // reset errors if we are here
    rc.fetchErrorsCount = 0
    
    // Convert string values to integers
    data := make([]int, rc.statsCapacity)

    for i, v := range values {
        val, err := strconv.Atoi(strings.TrimSpace(v))
        if err != nil {
            log.Printf("Error converting value %s to int: %v", v, err)
            break
        }
        data[i] = val
    }
    
    rc.currentStats = map[string]map[string]int{
        "la":   {"total": data[0], "res": data[0]},
        "ram":  {"total": data[1], "res": data[2]},
        "disk": {"total": data[3], "res": data[4]},
        "net":  {"total": data[5], "res": data[6]},
    }

    return rc
}

func (rc *ResourceChecker) CheckLimits() *ResourceChecker {
    
    if rc.HasFetchErrorAlerts() {
        return rc
    }
    
    for param, data := range rc.currentStats {
        limit, exists := rc.limits[param]
        if !exists {
            continue
        }

        var result int

        switch limit.Type {
            case "scalar":
                result = data["res"]
            case "percent":
                if data["total"] > 0 {
                    result = (data["res"] * 100) / data["total"]
                }
            default:
                continue
        }

        if result >= limit.Value {
            formattedValue := rc.formatter.Format(param, data, result)
            rc.alerter.Alert(param, formattedValue)
        }
    }
    
    return rc
}

func main() {
    checker := NewResourceChecker(NewAlerter(), NewValueFormatter())
    checker.SetURI("http://srv.msk01.gigacorp.local/_stats")
    checker.SetLimits(`{ "la": { "type":"scalar", "value":30 }, "ram":{"type":"percent", "value":80}, "disk":{"type":"percent", "value":90}, "net":{"type":"percent", "value":90}}`)

    for {
        time.Sleep(1 * time.Second)
        checker.LoadInfo().CheckLimits()
    }
}
