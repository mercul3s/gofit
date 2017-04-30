package main

import (
  "fmt"
  "log"
  "bytes"
  "os"
  "strings"
  "time"
  "io/ioutil"
  "encoding/json"
  "encoding/base64"
  "net/http"
  "net/url"
  "text/template"
  "github.com/influxdata/influxdb/client/v2"
)

type FitbitApi struct {
  ClientId, ClientSecret, RedirectUri, AuthorizeUri, Scopes string
  Auth FitbitAuth
}

type FitbitAuth struct {
  AccessToken string `json:"access_token"`
  RefreshToken string `json:"refresh_token"`
  UserId string `json:"user_id"`
  TokenType string `json:"token_type"`
  Scope string `json:"scope"`
}

const (
  MyDB = "mydb"
  username = ""
  password = ""
)

func NewFitBitApi(ClientId string, ClientSecret string, RedirectUri string) FitbitApi {
  authorizeTmpl := "response_type=code&client_id={{.ClientId}}&redirect_uri={{.RedirectUri}}&scope={{.Scopes}}&expires_in=604800"
  tmpl, err := template.New("test").Parse(authorizeTmpl)
  api := FitbitApi{}
  api.ClientId = ClientId
  api.ClientSecret = ClientSecret
  api.RedirectUri = RedirectUri
  api.Scopes = "profile settings location heartrate activity weight sleep nutrition"
  if err != nil { panic(err) }
  var authorizeBuf bytes.Buffer
  err = tmpl.Execute(&authorizeBuf, api)
  if err != nil { panic(err) }
  api.AuthorizeUri = "https://www.fitbit.com/oauth2/authorize?" + url.PathEscape(authorizeBuf.String())
  return api
}

func (api *FitbitApi) EncodeBasicAuth() string{
  authstring := api.ClientId + ":" + api.ClientSecret
  return base64.StdEncoding.EncodeToString([]byte(authstring))
}

func (api *FitbitApi) GetProfile() string{
  req, _ := http.NewRequest("GET", "https://api.fitbit.com/1/user/-/profile.json", nil)
  req.Header.Set("Authorization", "Bearer " + api.Auth.AccessToken)
  res, _ := http.DefaultClient.Do(req)
  profiledata, _:= ioutil.ReadAll(res.Body)
  res.Body.Close()
  return string(profiledata)
}

type ActivitySteps struct {
  Steps []DataPoint `json:"activities-steps"`
}

type DataPoint struct {
  Time string `json:"dateTime"`
  Value string `json:"value"`
}

func (api *FitbitApi) GetActivitySteps() ActivitySteps{
  req, _ := http.NewRequest("GET", "https://api.fitbit.com/1/user/-/activities/steps/date/today/1y.json", nil)
  req.Header.Set("Authorization", "Bearer " + api.Auth.AccessToken)
  res, _ := http.DefaultClient.Do(req)
  var activitySteps ActivitySteps
  decoder := json.NewDecoder(res.Body)
  decerr := decoder.Decode(&activitySteps)
  if decerr != nil { panic(decerr) }
  res.Body.Close()
  return activitySteps
}

type HeartDataPoint struct {
  Time string `json:"dateTime"`
  Value HeartDataValue `json:"value"`
}

type HeartDataValue struct {
  RestingHeartRate int `json:"restingHeartRate"`
}

type ActivityHeart struct {
  HeartData []HeartDataPoint `json:"activities-heart"`
}

func (api *FitbitApi) GetRestingHeartrate() ActivityHeart{
  req, _ := http.NewRequest("GET", "https://api.fitbit.com/1/user/-/activities/heart/date/today/1y.json", nil)
  req.Header.Set("Authorization", "Bearer " + api.Auth.AccessToken)
  res, _ := http.DefaultClient.Do(req)
  var activityHeart ActivityHeart
  decoder := json.NewDecoder(res.Body)
  decerr := decoder.Decode(&activityHeart)
  if decerr != nil { panic(decerr) }
  res.Body.Close()
  return activityHeart
}

func (api *FitbitApi) LoadAccessToken(code string){
  form := url.Values{}
  form.Add("clientId", api.ClientId)
  form.Add("grant_type", "authorization_code")
  form.Add("redirect_uri", api.RedirectUri)
  form.Add("code", code)
  req, err := http.NewRequest("POST", "https://api.fitbit.com/oauth2/token", strings.NewReader(form.Encode()))
  req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
  req.Header.Set("Authorization", "Basic " + api.EncodeBasicAuth())
  res, err := http.DefaultClient.Do(req)
  if err != nil { panic(err) }
  decoder := json.NewDecoder(res.Body)

  var auth FitbitAuth
  decerr := decoder.Decode(&auth)
  if decerr != nil { panic(decerr) }
  res.Body.Close()
  api.Auth = auth
}

func (api *FitbitApi) loadInfluxData(){
  fmt.Println("Loading step data into influxdb...")
  activitySteps := api.GetActivitySteps()

  c, err := client.NewHTTPClient(client.HTTPConfig{
    Addr:     "http://localhost:8086",
    Username: username,
    Password: password,
  })
  if err != nil {
    log.Fatal(err)
  }

  bp, err := client.NewBatchPoints(client.BatchPointsConfig{
    Database:  MyDB,
    Precision: "s",
  })
  if err != nil {
    log.Fatal(err)
  }

  for _, v := range activitySteps.Steps {
    t1, e := time.Parse("2006-01-02", v.Time)
    if e != nil {
      log.Fatal(e)
    }
    tags := map[string]string{"steps": "steps-total"}
    fields := map[string]interface{}{
      "steps":  v.Value,
    }
    pt, err := client.NewPoint("activity_steps", tags, fields, t1)
    if err != nil {
      log.Fatal(err)
    }
    bp.AddPoint(pt)
  }

  // Write the batch
  if err := c.Write(bp); err != nil {
    log.Fatal(err)
  }
  fmt.Println("Done loading steps")

  fmt.Println("Loading heartrate data")
  activityHeart := api.GetRestingHeartrate()

  bp, err2 := client.NewBatchPoints(client.BatchPointsConfig{
    Database:  MyDB,
    Precision: "s",
  })
  if err2 != nil {
    log.Fatal(err)
  }

  for _, v := range activityHeart.HeartData {
    t1, e := time.Parse("2006-01-02", v.Time)
    if e != nil {
      log.Fatal(e)
    }
    fmt.Printf("%s: %dBPM \n", t1, v.Value.RestingHeartRate)
    tags := map[string]string{"heart": "resting-heart"}
    fields := map[string]interface{}{
      "resting":  v.Value.RestingHeartRate,
    }
    pt, err := client.NewPoint("heart", tags, fields, t1)
    if err != nil {
      log.Fatal(err)
    }
    bp.AddPoint(pt)
  }

  // Write the batch
  if err := c.Write(bp); err != nil {
    log.Fatal(err)
  }
  fmt.Println("Done")
}

func main() {
  mux := http.NewServeMux()
  api := NewFitBitApi(os.Getenv("FITBIT_CLIENT_ID"), os.Getenv("FITBIT_CLIENT_SECRET"), "http://localhost:4000/auth")

  mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
    code := r.URL.Query()["code"][0]
    api.LoadAccessToken(code)
    fmt.Fprintf(w, api.GetProfile())

    api.loadInfluxData()
  })

  mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Add("Content-Type", "text/html")
    fmt.Fprintf(w, "Visit: <a href=%q>%q</a>", api.AuthorizeUri, api.AuthorizeUri)
  })

  fmt.Println("Visit: " + api.AuthorizeUri)
  log.Fatal(http.ListenAndServe(":4000", mux))

}
