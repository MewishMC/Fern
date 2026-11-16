package modules

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// WeatherStatus is what the panel shows about the weather lookup.
type WeatherStatus struct {
	State     string `json:"state"` // see the weather* state constants
	Place     string `json:"place"`
	Line      string `json:"line"`
	UpdatedMS int64  `json:"updatedMs"`
}

// Weather lookup states.
const (
	weatherOff      = "off"
	weatherNoCity   = "nocity"
	weatherLocating = "locating"
	weatherNotFound = "notfound"
	weatherOK       = "ok"
	weatherOffline  = "offline"
	weatherLimited  = "limited"
)

// Open-Meteo endpoints. Neither needs a key. Variables so tests can use a
// local server.
var (
	weatherGeoBase      = "https://geocoding-api.open-meteo.com/v1/search"
	weatherForecastBase = "https://api.open-meteo.com/v1/forecast"
)

// weatherNow is the current conditions, always in °C and km/h; units are
// converted when the line is drawn, so changing them needs no new request.
type weatherNow struct {
	Temp     float64
	Feels    float64
	Humidity float64
	Wind     float64
	Code     int
	Day      bool
	At       time.Time
}

type weatherPlace struct {
	name     string
	lat, lon float64
}

var (
	weatherMu      sync.RWMutex
	weatherCity    string // the City the state below belongs to
	weatherState   = weatherOff
	weatherAt      weatherPlace
	weatherHave    bool // weatherAt is resolved
	weatherData    weatherNow
	weatherFetched time.Time // last successful forecast
	weatherRetryAt time.Time
	weatherFails   int
)

// weatherStale is when old conditions stop being shown.
const weatherStale = 3 * time.Hour

func startWeatherWorker() {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			weatherTick(time.Now())
			<-ticker.C
		}
	}()
}

// weatherTick finds the place once per city name and refreshes the forecast
// every IntervalMin minutes, backing off after failures. It runs on the
// weather worker only, so a slow network never holds up the chatbox.
func weatherTick(now time.Time) {
	cfg := GetConfig()
	o := cfg.WeatherOptions
	city := strings.TrimSpace(o.City)
	weatherMu.Lock()
	if !cfg.DisplayOptions.Weather {
		weatherState = weatherOff
		weatherMu.Unlock()
		return
	}
	if city != weatherCity {
		weatherCity, weatherHave, weatherFails = city, false, 0
		weatherData, weatherFetched, weatherRetryAt = weatherNow{}, time.Time{}, time.Time{}
		weatherState = weatherLocating
	}
	if city == "" {
		weatherState = weatherNoCity
		weatherMu.Unlock()
		return
	}
	if weatherState == weatherNotFound || now.Before(weatherRetryAt) {
		weatherMu.Unlock()
		return
	}
	have, place := weatherHave, weatherAt
	due := now.Sub(weatherFetched) >= time.Duration(o.IntervalMin)*time.Minute
	weatherMu.Unlock()

	if !have {
		p, found, err := locateWeather(city)
		if !weatherStore(city, now, err, func() {
			if !found {
				weatherState = weatherNotFound
				return
			}
			weatherAt, weatherHave = p, true
		}) || !found {
			return
		}
		place, due = p, true
	}
	if !due {
		return
	}
	w, err := fetchWeather(place)
	weatherStore(city, now, err, func() {
		w.At = now
		weatherData, weatherFetched, weatherState = w, now, weatherOK
	})
}

// weatherStore applies the result of a request made for city, unless the
// city changed meanwhile. Failures schedule a retry that backs off from one
// minute to the refresh interval. It reports whether the request worked.
func weatherStore(city string, now time.Time, err error, apply func()) bool {
	weatherMu.Lock()
	defer weatherMu.Unlock()
	if city != weatherCity {
		return false
	}
	if err != nil {
		weatherFails++
		wait := time.Minute << min(weatherFails-1, 4)
		if limit := time.Duration(GetConfig().WeatherOptions.IntervalMin) * time.Minute; wait > limit {
			wait = limit
		}
		weatherRetryAt = now.Add(wait)
		if weatherState != weatherOK || now.Sub(weatherFetched) > weatherStale {
			weatherState = weatherOffline
			if err == errRateLimited {
				weatherState = weatherLimited
			}
		}
		return false
	}
	weatherFails = 0
	apply()
	return true
}

var weatherCoords = regexp.MustCompile(`^\s*(-?\d{1,2}(?:\.\d+)?)\s*,\s*(-?\d{1,3}(?:\.\d+)?)\s*$`)

// locateWeather turns the city setting into coordinates. "lat, lon" is used
// as is; otherwise the name is looked up, and anything after a comma ("Paris,
// Texas" or "London, GB") picks between places of the same name.
func locateWeather(city string) (weatherPlace, bool, error) {
	if m := weatherCoords.FindStringSubmatch(city); m != nil {
		lat, _ := strconv.ParseFloat(m[1], 64)
		lon, _ := strconv.ParseFloat(m[2], 64)
		if lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 {
			return weatherPlace{name: fmt.Sprintf("%.2f, %.2f", lat, lon), lat: lat, lon: lon}, true, nil
		}
	}
	name, hint, _ := strings.Cut(city, ",")
	name, hint = strings.TrimSpace(name), strings.ToLower(strings.TrimSpace(hint))
	v := url.Values{}
	v.Set("name", name)
	v.Set("count", "10")
	v.Set("language", "en")
	v.Set("format", "json")
	var reply struct {
		Results []struct {
			Name        string  `json:"name"`
			Latitude    float64 `json:"latitude"`
			Longitude   float64 `json:"longitude"`
			Country     string  `json:"country"`
			CountryCode string  `json:"country_code"`
			Admin1      string  `json:"admin1"`
		} `json:"results"`
	}
	status, _, err := getJSON(weatherGeoBase+"?"+v.Encode(), &reply)
	if err != nil {
		return weatherPlace{}, false, err
	}
	if status != 200 {
		return weatherPlace{}, false, nil
	}
	for _, r := range reply.Results {
		if hint != "" && !strings.Contains(strings.ToLower(r.Country+" "+r.CountryCode+" "+r.Admin1), hint) {
			continue
		}
		label := joinNonEmpty(", ", r.Name, r.Admin1, r.Country)
		return weatherPlace{name: label, lat: r.Latitude, lon: r.Longitude}, true, nil
	}
	return weatherPlace{}, false, nil
}

// fetchWeather reads the current conditions for a place.
func fetchWeather(p weatherPlace) (weatherNow, error) {
	v := url.Values{}
	v.Set("latitude", strconv.FormatFloat(p.lat, 'f', 4, 64))
	v.Set("longitude", strconv.FormatFloat(p.lon, 'f', 4, 64))
	v.Set("current", "temperature_2m,apparent_temperature,relative_humidity_2m,weather_code,wind_speed_10m,is_day")
	v.Set("wind_speed_unit", "kmh")
	v.Set("timezone", "auto")
	var reply struct {
		Current *struct {
			Temp     float64 `json:"temperature_2m"`
			Feels    float64 `json:"apparent_temperature"`
			Humidity float64 `json:"relative_humidity_2m"`
			Code     int     `json:"weather_code"`
			Wind     float64 `json:"wind_speed_10m"`
			IsDay    int     `json:"is_day"`
		} `json:"current"`
	}
	status, _, err := getJSON(weatherForecastBase+"?"+v.Encode(), &reply)
	if err != nil {
		return weatherNow{}, err
	}
	if status != 200 || reply.Current == nil {
		return weatherNow{}, httpStatusError(status)
	}
	c := reply.Current
	return weatherNow{Temp: c.Temp, Feels: c.Feels, Humidity: c.Humidity, Wind: c.Wind, Code: c.Code, Day: c.IsDay != 0}, nil
}

// weatherCondition names a WMO weather code and gives it an emoji.
func weatherCondition(code int, day bool) (text, emoji string) {
	switch code {
	case 0:
		if day {
			return "Clear", "☀️"
		}
		return "Clear", "🌙"
	case 1:
		if day {
			return "Mostly clear", "🌤️"
		}
		return "Mostly clear", "🌙"
	case 2:
		if day {
			return "Partly cloudy", "⛅"
		}
		return "Partly cloudy", "☁️"
	case 3:
		return "Overcast", "☁️"
	case 45, 48:
		return "Fog", "🌫️"
	case 51, 53, 55:
		return "Drizzle", "🌦️"
	case 56, 57:
		return "Freezing drizzle", "🌧️"
	case 61:
		return "Light rain", "🌧️"
	case 63:
		return "Rain", "🌧️"
	case 65:
		return "Heavy rain", "🌧️"
	case 66, 67:
		return "Freezing rain", "🌧️"
	case 71:
		return "Light snow", "🌨️"
	case 73:
		return "Snow", "🌨️"
	case 75:
		return "Heavy snow", "❄️"
	case 77:
		return "Snow grains", "🌨️"
	case 80, 81:
		return "Showers", "🌦️"
	case 82:
		return "Heavy showers", "🌧️"
	case 85, 86:
		return "Snow showers", "🌨️"
	case 95:
		return "Thunderstorm", "⛈️"
	case 96, 99:
		return "Thunderstorm, hail", "⛈️"
	}
	return "", "🌡️"
}

// formatWeather draws the conditions with the chosen parts and units, like
// "⛅ Partly cloudy 12°C · feels 10°C · 💧80% · 💨12 km/h".
func formatWeather(o WeatherOptions, w weatherNow) string {
	text, emoji := weatherCondition(w.Code, w.Day)
	temp := func(c float64) string {
		unit := "°C"
		if o.TempUnit == "f" {
			c, unit = c*9/5+32, "°F"
		}
		return weatherNumber(c, o.Decimals) + unit
	}
	var head []string
	if o.ShowEmoji {
		head = append(head, emoji)
	}
	if o.ShowCondition {
		head = append(head, text)
	}
	if o.ShowTemp {
		head = append(head, temp(w.Temp))
	}
	parts := []string{strings.Join(head, " ")}
	if o.ShowFeels {
		parts = append(parts, "feels "+temp(w.Feels))
	}
	if o.ShowHumidity {
		parts = append(parts, "💧"+weatherNumber(w.Humidity, 0)+"%")
	}
	if o.ShowWind {
		wind, unit := w.Wind, "km/h"
		switch o.WindUnit {
		case "mph":
			wind, unit = wind/1.609344, "mph"
		case "ms":
			wind, unit = wind/3.6, "m/s"
		}
		parts = append(parts, "💨"+weatherNumber(wind, o.Decimals)+" "+unit)
	}
	return joinNonEmpty(" · ", parts...)
}

// weatherNumber rounds v to the given decimals without printing "-0".
func weatherNumber(v float64, decimals int) string {
	p := math.Pow(10, float64(decimals))
	v = math.Round(v*p) / p
	if v == 0 {
		v = 0 // turns -0 into 0
	}
	return strconv.FormatFloat(v, 'f', decimals, 64)
}

// weatherText is the current weather for the chatbox, or "" while there is
// nothing recent to show.
func weatherText(cfg Config) string {
	if !cfg.DisplayOptions.Weather {
		return ""
	}
	weatherMu.RLock()
	w, fetched, city := weatherData, weatherFetched, weatherCity
	weatherMu.RUnlock()
	if fetched.IsZero() || time.Since(fetched) > weatherStale || city != strings.TrimSpace(cfg.WeatherOptions.City) {
		return ""
	}
	return formatWeather(cfg.WeatherOptions, w)
}

// weatherMerged reports whether the weather goes on the time line.
func weatherMerged(cfg Config) bool {
	return cfg.WeatherOptions.InTime && cfg.DisplayOptions.Time
}

// weatherLine is the weather module's own line, empty while it is merged
// into the time line.
func weatherLine(cfg Config) string {
	if weatherMerged(cfg) {
		return ""
	}
	return withLabel(cfg.WeatherOptions.Label, weatherText(cfg))
}

// CurrentWeatherStatus reports the lookup for the panel.
func CurrentWeatherStatus() WeatherStatus {
	cfg := GetConfig()
	weatherMu.RLock()
	st := WeatherStatus{State: weatherState, UpdatedMS: 0}
	if weatherHave {
		st.Place = weatherAt.name
	}
	if !weatherFetched.IsZero() {
		st.UpdatedMS = weatherFetched.UnixMilli()
	}
	weatherMu.RUnlock()
	switch {
	case !cfg.DisplayOptions.Weather:
		st.State = weatherOff
	case strings.TrimSpace(cfg.WeatherOptions.City) == "":
		st.State, st.Place = weatherNoCity, ""
	case st.State == weatherOff:
		// Just switched on; the worker gets to it within a few seconds.
		st.State = weatherLocating
	}
	st.Line = weatherText(cfg)
	return st
}

// sanitizeWeather keeps the weather options in range.
func sanitizeWeather(o *WeatherOptions) {
	o.City = RuneLimit(strings.Join(strings.Fields(o.City), " "), 80)
	o.Label = RuneLimit(strings.TrimSpace(o.Label), 16)
	if o.TempUnit != "f" {
		o.TempUnit = "c"
	}
	switch o.WindUnit {
	case "kmh", "mph", "ms":
	default:
		o.WindUnit = "kmh"
	}
	o.Decimals = clampInt(o.Decimals, 0, 1)
	o.IntervalMin = clampInt(o.IntervalMin, 10, 60)
}
