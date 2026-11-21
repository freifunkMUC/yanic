package data

// ResponseData struct
type ResponseData struct {
	Neighbours   *Neighbours            `json:"neighbours"`
	Nodeinfo     *Nodeinfo              `json:"nodeinfo"`
	Statistics   *Statistics            `json:"statistics"`
	CustomFields map[string]interface{} `json:"-"`
	Metrics      map[string]float64     `json:"-"`
}
