package managerapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"github.com/gorilla/mux"
	managerdb "github.com/spiffe/tornjak/pkg/manager/db"
)

const (
	keyShowLen  int = 40
	certShowLen int = 50
)

type Server struct {
	listenAddr string
	db         managerdb.ManagerDB
}

// Handle preflight checks
func (s *Server) corsHandler(f http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if r.Method == "OPTIONS" {
            s.cors(w, r)  
            return
        }
        f(w, r)
    }
}

// func cors(w http.ResponseWriter, _ *http.Request) {
// 	w.Header().Set("Content-Type", "text/html; charset=ascii")
// 	w.Header().Set("Access-Control-Allow-Origin", "*")
// 	w.Header().Set("Access-Control-Allow-Headers", "Content-Type,access-control-allow-origin, access-control-allow-headers")
// 	w.WriteHeader(http.StatusOK)
// }

func (s *Server) cors(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=ascii")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type,access-control-allow-origin, access-control-allow-headers")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) retError(w http.ResponseWriter, emsg string, status int) {
    w.Header().Set("Content-Type", "text/html; charset=ascii")
    w.Header().Set("Access-Control-Allow-Origin", "*")
    w.Header().Set("Access-Control-Allow-Headers", "Content-Type,access-control-allow-origin, access-control-allow-headers")
    http.Error(w, emsg, status)
}


func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// Returns a post proxy function for tornjak api, where path is the path from the base URL, i.e. "/api/entry/delete"

/*
	Start refactoring code into methods
*/

const (
    ApiVersion  = "v1" 
    RetryCount  = 3    // Number of times to retry failed API requests
    RetryDelay  = 500 * time.Millisecond
)

// Main function that calls the helpers
func (s *Server) apiServerProxyFunc(apiPath string, apiMethod string) func(w http.ResponseWriter, r *http.Request) {
    return func(w http.ResponseWriter, r *http.Request) {
        serverName := mux.Vars(r)["server"]
        sinfo, err := s.getServerInfo(serverName, w)
        if err != nil {
            emsg := fmt.Sprintf("Error getting server info: %v", err.Error())
    		retError(w, emsg, http.StatusBadRequest)
    		return
        }

        client := s.getHttpClient()
        s.proxyRequest(client, sinfo, apiPath, apiMethod, w, r)
    }
}

// Get server info helper function
func (s *Server) getServerInfo(serverName string, w http.ResponseWriter) (*ServerInfo, error) {
    sinfo, err := s.db.GetServer(serverName)
    if err != nil {
        s.retError(w, fmt.Sprintf("Error getting server info: %v", err), http.StatusBadRequest)
        return nil, err
    }
    return sinfo, nil
}

// Proxy function with retries
func (s *Server) proxyRequestWithRetry(client *http.Client, sinfo *ServerInfo, apiPath, apiMethod string, w http.ResponseWriter, r *http.Request) {
    // Read request body (since it can be read only once)
    var bodyBytes []byte
    if r.Body != nil {
        var err error
        bodyBytes, err = io.ReadAll(r.Body)
        if err != nil {
            s.handleError(w, "Failed to read request body", http.StatusInternalServerError, err)
            return
        }
    }

	// Handle retry logic here
	// Retry logic
    for attempt := 0; attempt <= RetryCount; attempt++ {
        if attempt > 0 {
            time.Sleep(RetryDelay)
        }

        err := s.forwardRequest(client, sinfo, apiPath, apiMethod, w, r, bodyBytes)
        if err == nil {
            return // success
        }

        log.Printf("Attempt %d failed: %v\n", attempt+1, err)
    }
    s.handleError(w, "All retries failed", http.StatusBadGateway, nil)
}

// Core request forwarding logic
func (s *Server) forwardRequest(client *http.Client, sinfo *ServerInfo, apiPath, apiMethod string, w http.ResponseWriter, r *http.Request, body []byte) error {
    url := fmt.Sprintf("%s/%s%s", strings.TrimSuffix(sinfo.Address, "/"), ApiVersion, apiPath)

    req, err := http.NewRequest(apiMethod, url, bytes.NewReader(body))
    if err != nil {
        return fmt.Errorf("error creating request: %w", err)
    }

    req.Header = r.Header.Clone()

    // more logic for forwarding logic
	resp, err := client.Do(req)
    if err != nil {
        return fmt.Errorf("error sending request to server: %w", err)
    }
    defer resp.Body.Close()

    copyHeader(w.Header(), resp.Header)
    w.WriteHeader(resp.StatusCode)
    _, err = io.Copy(w, resp.Body)
    if err != nil {
        return fmt.Errorf("error copying response body: %w", err)
    }

    return nil
}

// Error handler function
func (s *Server) handleError(w http.ResponseWriter, msg string, status int, err error) {
    if err != nil {
        msg = fmt.Sprintf("%s: %v", msg, err)
    }
    log.Println(msg)
    s.retError(w, msg, status)
}


// func (s *Server) apiServerProxyFunc(apiPath string, apiMethod string) func(w http.ResponseWriter, r *http.Request) {
// 	return func(w http.ResponseWriter, r *http.Request) {
// 		vars := mux.Vars(r)
// 		serverName := vars["server"]

// 		fmt.Println(serverName)

// 		// Get server info
// 		sinfo, err := s.db.GetServer(serverName)
// 		if err != nil {
// 			emsg := fmt.Sprintf("Error getting server info: %v", err.Error())
// 			retError(w, emsg, http.StatusBadRequest)
// 			return
// 		}

// 		// Gather the certs and key into a map
// 		cMap := make(map[string]string)
// 		cMap["CA"] = string(sinfo.CA)
// 		cMap["cert"] = string(sinfo.Cert)
// 		cMap["key"] = string(sinfo.Key)

// 		// Iterate through the map and trim the values for debugging.
// 		// Show the endings only
// 		for k, v := range cMap {
// 			if k == "key" {
// 				if len(v) > keyShowLen {
// 					cMap[k] = "\n..." + v[len(v)-keyShowLen:]
// 				}
// 			} else {
// 				if len(v) > certShowLen {
// 					cMap[k] = "\n..." + v[len(v)-certShowLen:]
// 				}
// 			}
// 		}
// 		fmt.Printf("Name:%s\n Address:%s\n TLS:%t, mTLS:%t\n", sinfo.Name, sinfo.Address, sinfo.TLS, sinfo.MTLS)
// 		if sinfo.TLS {
// 			fmt.Printf("CA:%s\n", cMap["CA"])
// 		}
// 		if sinfo.MTLS {
// 			fmt.Printf("Cert:%s\n Key:%s\n", cMap["cert"], cMap["key"])
// 		}

// 		client, err := sinfo.HttpClient()
// 		if err != nil {
// 			emsg := fmt.Sprintf("Error initializing server client: %v", err.Error())
// 			retError(w, emsg, http.StatusBadRequest)
// 			return
// 		}

// 		req, err := http.NewRequest(apiMethod, strings.TrimSuffix(sinfo.Address, "/") + apiPath, r.Body)
// 		if err != nil {
// 			emsg := fmt.Sprintf("Error creating http request: %v", err.Error())
// 			retError(w, emsg, http.StatusBadRequest)
// 			return
// 		}

		
// 		resp, err := client.Do(req)
// 		if err != nil {
// 			emsg := fmt.Sprintf("Error making api call to server: %v", err.Error())
// 			retError(w, emsg, http.StatusBadRequest)
// 			return
// 		}
// 		defer resp.Body.Close()
// 		copyHeader(w.Header(), resp.Header)
// 		w.WriteHeader(resp.StatusCode)
// 		_, err = io.Copy(w, resp.Body)
// 		if err != nil {
// 			emsg := fmt.Sprintf("Error parsing data: %v", err.Error())
// 			retError(w, emsg, http.StatusBadRequest)
// 			return
// 		}
// 	}
// }

// spaHandler implements the http.Handler interface, so we can use it
// to respond to HTTP requests. The path to the static directory and
// path to the index file within that static directory are used to
// serve the SPA in the given static directory.
type spaHandler struct {
	staticPath string
	indexPath  string
}

// ServeHTTP inspects the URL path to locate a file within the static dir
// on the SPA handler. If a file is found, it will be served. If not, the
// file located at the index path on the SPA handler will be served. This
// is suitable behavior for serving an SPA (single page application).
func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// get the absolute path to prevent directory traversal
	path, err := filepath.Abs(r.URL.Path)
	if err != nil {
		// if we failed to get the absolute path respond with a 400 bad request
		// and stop
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// prepend the path with the path to the static directory
	path = filepath.Join(h.staticPath, path)

	// check whether a file exists at the given path
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		// file does not exist, serve index.html
		http.ServeFile(w, r, filepath.Join(h.staticPath, h.indexPath))
		return
	} else if err != nil {
		// if we got an error (that wasn't that the file doesn't exist) stating the
		// file, return a 500 internal server error and stop
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// otherwise, use http.FileServer to serve the static dir
	http.FileServer(http.Dir(h.staticPath)).ServeHTTP(w, r)
}

func (s *Server) HandleRequests() {
	// TO implement
	rtr := mux.NewRouter()

	// Manger-specific
	rtr.HandleFunc("/manager-api/server/list", corsHandler(s.serverList))
	rtr.HandleFunc("/manager-api/server/register", corsHandler(s.serverRegister))

	// SPIRE server info calls
	rtr.HandleFunc("/manager-api/healthcheck/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/spire/healthcheck", http.MethodGet)))
	rtr.HandleFunc("/manager-api/serverinfo/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/spire/serverinfo", http.MethodGet)))

	// Entries
	rtr.HandleFunc("/manager-api/entry/list/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/spire/entries", http.MethodGet)))
	rtr.HandleFunc("/manager-api/entry/delete/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/spire/entries", http.MethodDelete)))
	rtr.HandleFunc("/manager-api/entry/create/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/spire/entries", http.MethodPost)))

	// Agents
	rtr.HandleFunc("/manager-api/agent/list/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/spire/agents", http.MethodGet)))
	rtr.HandleFunc("/manager-api/agent/delete/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/spire/agents", http.MethodDelete)))
	rtr.HandleFunc("/manager-api/agent/ban/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/spire/agents/ban", http.MethodPost)))
	rtr.HandleFunc("/manager-api/agent/createjointoken/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/spire/agents/jointoken", http.MethodPost)))

	// Tornjak-specific
	rtr.HandleFunc("/manager-api/tornjak/serverinfo/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/tornjak/serverinfo", http.MethodGet)))
	// Agents Selectors
	rtr.HandleFunc("/manager-api/tornjak/selectors/register/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/tornjak/selectors", http.MethodPost)))
	rtr.HandleFunc("/manager-api/tornjak/selectors/list/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/tornjak/selectors", http.MethodGet)))
	rtr.HandleFunc("/manager-api/tornjak/agents/list/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/tornjak/agents", http.MethodGet)))
	// Agents Clusters
	rtr.HandleFunc("/manager-api/tornjak/clusters/create/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/tornjak/clusters", http.MethodPost)))
	rtr.HandleFunc("/manager-api/tornjak/clusters/edit/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/tornjak/clusters", http.MethodPatch)))
	rtr.HandleFunc("/manager-api/tornjak/clusters/list/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/tornjak/clusters", http.MethodGet)))
	rtr.HandleFunc("/manager-api/tornjak/clusters/delete/{server:.*}", corsHandler(s.apiServerProxyFunc("/api/v1/tornjak/clusters", http.MethodDelete)))

	//http.HandleFunc("/manager-api/get-server-info", s.agentList)
	//http.HandleFunc("/manager-api/agent/list/:id", s.agentList)

	spa := spaHandler{staticPath: "ui-manager", indexPath: "index.html"}
	rtr.PathPrefix("/").Handler(spa)

	fmt.Println("Starting to listen...")
	log.Fatal(http.ListenAndServe(s.listenAddr, rtr))
}

/*

func main() {
  rtr := mux.NewRouter()
  rtr.HandleFunc("/number/{id:[0-9]+}", pageHandler)
  http.Handle("/", rtr)
  http.ListenAndServe(PORT, nil)
}
*/

// // NewManagerServer returns a new manager server, given a listening address for the
// // server, and a DB connection string

// readRequestBody reads and returns the request body as a string.
func readRequestBody(r *http.Request) (string, error) {
	buf := new(strings.Builder)
	_, err := io.Copy(buf, r.Body)
	return buf.String(), err
}

// parseJSON parses the given string into a generic type T.
func parseJSON[T any](data string) (T, error) {
	var input T
	if strings.TrimSpace(data) == "" {
		return input, nil
	}
	err := json.Unmarshal([]byte(data), &input)
	return input, err
}

// writeJSON encodes and writes a JSON response with proper headers.
func writeJSON(w http.ResponseWriter, v any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(v)
}

// New serverList
func (s *Server) serverList(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Endpoint Hit: Server List")

	data, err := readRequestBody(r)
	if err != nil {
		s.retError(w, "Error reading request body", http.StatusBadRequest)
		return
	}

	input, err := parseJSON[ListServersRequest](data)
	if err != nil {
		s.retError(w, fmt.Sprintf("Error parsing JSON: %v", err), http.StatusBadRequest)
		return
	}

	ret, err := s.ListServers(input)
	if err != nil {
		s.retError(w, fmt.Sprintf("Error listing servers: %v", err), http.StatusBadRequest)
		return
	}

	cors(w, r)
	if err := writeJSON(w, ret); err != nil {
		s.retError(w, fmt.Sprintf("Error encoding response: %v", err), http.StatusBadRequest)
	}
}

func (s *Server) serverRegister(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Endpoint Hit: Server Create")

	data, err := readRequestBody(r)
	if err != nil {
		s.retError(w, "Error reading request body", http.StatusBadRequest)
		return
	}

	input, err := parseJSON[RegisterServerRequest](data)
	if err != nil {
		s.retError(w, fmt.Sprintf("Error parsing JSON: %v", err), http.StatusBadRequest)
		return
	}

	if err := s.RegisterServer(input); err != nil {
		s.retError(w, fmt.Sprintf("Error registering server: %v", err), http.StatusBadRequest)
		return
	}

	// TODO CORS function
}

// func NewManagerServer(listenAddr, dbString string) (*Server, error) {
// 	db, err := managerdb.NewLocalSqliteDB(dbString)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &Server{
// 		listenAddr: listenAddr,
// 		db:         db,
// 	}, nil
// }

// func (s *Server) serverList(w http.ResponseWriter, r *http.Request) {
// 	fmt.Println("Endpoint Hit: Server List")

// 	buf := new(strings.Builder)

// 	n, err := io.Copy(buf, r.Body)
// 	if err != nil {
// 		emsg := fmt.Sprintf("Error parsing data: %v", err.Error())
// 		retError(w, emsg, http.StatusBadRequest)
// 		return
// 	}
// 	data := buf.String()

// 	var input ListServersRequest
// 	if n == 0 {
// 		input = ListServersRequest{}
// 	} else {
// 		err := json.Unmarshal([]byte(data), &input)
// 		if err != nil {
// 			emsg := fmt.Sprintf("Error parsing data: %v", err.Error())
// 			retError(w, emsg, http.StatusBadRequest)
// 			return
// 		}
// 	}

// 	ret, err := s.ListServers(input)
// 	if err != nil {
// 		emsg := fmt.Sprintf("Error: %v", err.Error())
// 		retError(w, emsg, http.StatusBadRequest)
// 		return
// 	}
// 	cors(w, r)

// 	je := json.NewEncoder(w)
// 	err = je.Encode(ret)

// 	if err != nil {
// 		emsg := fmt.Sprintf("Error: %v", err.Error())
// 		retError(w, emsg, http.StatusBadRequest)
// 		return
// 	}
// }

// func (s *Server) serverRegister(w http.ResponseWriter, r *http.Request) {
// 	fmt.Println("Endpoint Hit: Server Create")

// 	buf := new(strings.Builder)

// 	n, err := io.Copy(buf, r.Body)
// 	if err != nil {
// 		emsg := fmt.Sprintf("Error parsing data: %v", err.Error())
// 		retError(w, emsg, http.StatusBadRequest)
// 		return
// 	}
// 	data := buf.String()

// 	var input RegisterServerRequest
// 	if n == 0 {
// 		input = RegisterServerRequest{}
// 	} else {
// 		err := json.Unmarshal([]byte(data), &input)
// 		if err != nil {
// 			emsg := fmt.Sprintf("Error parsing data: %v", err.Error())
// 			retError(w, emsg, http.StatusBadRequest)
// 			return
// 		}
// 	}

// 	err = s.RegisterServer(input)
// 	if err != nil {
// 		emsg := fmt.Sprintf("Error: %v", err.Error())
// 		retError(w, emsg, http.StatusBadRequest)
// 		return
// 	}

// 	cors(w, r)
// 	_, err = w.Write([]byte("SUCCESS"))

// 	if err != nil {
// 		emsg := fmt.Sprintf("Error: %v", err.Error())
// 		retError(w, emsg, http.StatusBadRequest)
// 		return
// 	}
// }
