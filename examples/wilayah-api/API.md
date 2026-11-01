# Indonesia Wilayah API

API documentation for Indonesia Wilayah API, managed by merapi.

**Version:** 0.1.0

**Servers:**
- `http://127.0.0.1:9090`

## Endpoints

### cities

| Method | Path | Summary | Responses |
|---|---|---|---|
| `get` | `/cities` | List cities | 200, 400 |
| `get` | `/cities/{id}` | Get a city | 200, 404 |

### provinces

| Method | Path | Summary | Responses |
|---|---|---|---|
| `get` | `/provinces` | List provinces | 200, 400 |
| `get` | `/provinces/{id}` | Get a province | 200, 404 |
| `get` | `/provinces/{id}/cities` | List cities in a province | 200, 404 |

## Endpoint details

### `get` /provinces

`operationId: getProvinces`

List provinces

Returns every province of Indonesia (BPS codes).

**Tags:** provinces

**Responses**

| Status | Description | Content type | Schema |
|---|---|---|---|
| `200` | OK | application/json | ProvinceList |
| `400` | BadRequest | application/json | Error |

**Request examples**

<details>
<summary>Code samples (cURL, Go, Python, JavaScript)</summary>

#### cURL

```bash
curl -X GET 'http://127.0.0.1:9090/provinces'
```

#### Go

```go
package main

import (
	"fmt"
	"io"
	"net/http"
)

func main() {
	req, err := http.NewRequest("GET", "http://127.0.0.1:9090/provinces", nil)
	if err != nil {
		panic(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	fmt.Println(resp.Status, string(data))
}
```

#### Python

```python
import requests

resp = requests.request(
    "GET",
    "http://127.0.0.1:9090/provinces",
)
print(resp.status_code, resp.text)
```

#### JavaScript

```javascript
const resp = await fetch("http://127.0.0.1:9090/provinces");

console.log(resp.status, await resp.text())
```

</details>


### `get` /provinces/{id}

`operationId: getProvincesById`

Get a province

Fetch one province by its numeric id (same as its BPS code).

**Tags:** provinces

**Parameters**

| Name | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | true | — |

**Responses**

| Status | Description | Content type | Schema |
|---|---|---|---|
| `200` | OK | application/json | Province |
| `404` | NotFound | application/json | Error |

**Request examples**

<details>
<summary>Code samples (cURL, Go, Python, JavaScript)</summary>

#### cURL

```bash
curl -X GET 'http://127.0.0.1:9090/provinces/<id>'
```

#### Go

```go
package main

import (
	"fmt"
	"io"
	"net/http"
)

func main() {
	req, err := http.NewRequest("GET", "http://127.0.0.1:9090/provinces/<id>", nil)
	if err != nil {
		panic(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	fmt.Println(resp.Status, string(data))
}
```

#### Python

```python
import requests

resp = requests.request(
    "GET",
    "http://127.0.0.1:9090/provinces/<id>",
)
print(resp.status_code, resp.text)
```

#### JavaScript

```javascript
const resp = await fetch("http://127.0.0.1:9090/provinces/<id>");

console.log(resp.status, await resp.text())
```

</details>


### `get` /provinces/{id}/cities

`operationId: getProvincesByIdCities`

List cities in a province

Returns the kabupaten/kota that belong to the province.

**Tags:** provinces

**Parameters**

| Name | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | true | — |

**Responses**

| Status | Description | Content type | Schema |
|---|---|---|---|
| `200` | OK | application/json | CityList |
| `404` | NotFound | application/json | Error |

**Request examples**

<details>
<summary>Code samples (cURL, Go, Python, JavaScript)</summary>

#### cURL

```bash
curl -X GET 'http://127.0.0.1:9090/provinces/<id>/cities'
```

#### Go

```go
package main

import (
	"fmt"
	"io"
	"net/http"
)

func main() {
	req, err := http.NewRequest("GET", "http://127.0.0.1:9090/provinces/<id>/cities", nil)
	if err != nil {
		panic(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	fmt.Println(resp.Status, string(data))
}
```

#### Python

```python
import requests

resp = requests.request(
    "GET",
    "http://127.0.0.1:9090/provinces/<id>/cities",
)
print(resp.status_code, resp.text)
```

#### JavaScript

```javascript
const resp = await fetch("http://127.0.0.1:9090/provinces/<id>/cities");

console.log(resp.status, await resp.text())
```

</details>


### `get` /cities

`operationId: getCities`

List cities

Returns every kabupaten/kota, optionally filtered by ?provinceId=.

**Tags:** cities

**Parameters**

| Name | In | Type | Required | Description |
|---|---|---|---|---|
| `provinceId` | query | integer | false | Filter cities by province id (BPS code). |

**Responses**

| Status | Description | Content type | Schema |
|---|---|---|---|
| `200` | OK | application/json | CityList |
| `400` | BadRequest | application/json | Error |

**Request examples**

<details>
<summary>Code samples (cURL, Go, Python, JavaScript)</summary>

#### cURL

```bash
curl -X GET 'http://127.0.0.1:9090/cities?provinceId=31'
```

#### Go

```go
package main

import (
	"fmt"
	"io"
	"net/http"
)

func main() {
	req, err := http.NewRequest("GET", "http://127.0.0.1:9090/cities?provinceId=31", nil)
	if err != nil {
		panic(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	fmt.Println(resp.Status, string(data))
}
```

#### Python

```python
import requests

resp = requests.request(
    "GET",
    "http://127.0.0.1:9090/cities?provinceId=31",
)
print(resp.status_code, resp.text)
```

#### JavaScript

```javascript
const resp = await fetch("http://127.0.0.1:9090/cities?provinceId=31");

console.log(resp.status, await resp.text())
```

</details>


### `get` /cities/{id}

`operationId: getCitiesById`

Get a city

Fetch one city (kabupaten/kota) by id.

**Tags:** cities

**Parameters**

| Name | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | true | — |

**Responses**

| Status | Description | Content type | Schema |
|---|---|---|---|
| `200` | OK | application/json | City |
| `404` | NotFound | application/json | Error |

**Request examples**

<details>
<summary>Code samples (cURL, Go, Python, JavaScript)</summary>

#### cURL

```bash
curl -X GET 'http://127.0.0.1:9090/cities/<id>'
```

#### Go

```go
package main

import (
	"fmt"
	"io"
	"net/http"
)

func main() {
	req, err := http.NewRequest("GET", "http://127.0.0.1:9090/cities/<id>", nil)
	if err != nil {
		panic(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	fmt.Println(resp.Status, string(data))
}
```

#### Python

```python
import requests

resp = requests.request(
    "GET",
    "http://127.0.0.1:9090/cities/<id>",
)
print(resp.status_code, resp.text)
```

#### JavaScript

```javascript
const resp = await fetch("http://127.0.0.1:9090/cities/<id>");

console.log(resp.status, await resp.text())
```

</details>

## Schemas

### `City`

- Type: `object`
- Properties: 4
- Required: `id`, `name`, `provinceId`, `type`

| Property | Type | Description |
|---|---|---|
| `id` | integer | — |
| `name` | string | — |
| `provinceId` | integer | — |
| `type` | string | — |


### `CityList`

- Type: `object`
- Properties: 2
- Required: `count`, `data`

| Property | Type | Description |
|---|---|---|
| `count` | integer | — |
| `data` | array | — |


### `Error`

- Type: `object`
- Properties: 2
- Required: `code`, `message`

| Property | Type | Description |
|---|---|---|
| `code` | integer | — |
| `message` | string | — |


### `Province`

- Type: `object`
- Properties: 3
- Required: `id`, `code`, `name`

| Property | Type | Description |
|---|---|---|
| `code` | string | — |
| `id` | integer | — |
| `name` | string | — |


### `ProvinceList`

- Type: `object`
- Properties: 2
- Required: `count`, `data`

| Property | Type | Description |
|---|---|---|
| `count` | integer | — |
| `data` | array | — |


---

_Generated by merapi._
