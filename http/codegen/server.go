package codegen

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"goa.design/goa/v3/codegen"
	"goa.design/goa/v3/codegen/service"
	"goa.design/goa/v3/expr"
)

// ServerFiles returns the generated HTTP server files.
func ServerFiles(genpkg string, root *expr.RootExpr) []*codegen.File {
	var files []*codegen.File
	for _, svc := range root.API.HTTP.Services {
		files = append(files, serverFile(genpkg, svc))
		if f := websocketServerFile(genpkg, svc); f != nil {
			files = append(files, f)
		}
	}
	for _, svc := range root.API.HTTP.Services {
		if f := serverEncodeDecodeFile(genpkg, svc); f != nil {
			files = append(files, f)
		}
	}
	return files
}

// server returns the file implementing the HTTP server.
func serverFile(genpkg string, svc *expr.HTTPServiceExpr) *codegen.File {
	data := HTTPServices.Get(svc.Name())
	svcName := data.Service.PathName
	path := filepath.Join(codegen.Gendir, "http", svcName, "server", "server.go")
	title := fmt.Sprintf("%s HTTP server", svc.Name())
	funcs := map[string]interface{}{
		"join":                    func(ss []string, s string) string { return strings.Join(ss, s) },
		"hasWebSocket":            hasWebSocket,
		"isWebSocketEndpoint":     isWebSocketEndpoint,
		"viewedServerBody":        viewedServerBody,
		"mustDecodeRequest":       mustDecodeRequest,
		"addLeadingSlash":         addLeadingSlash,
		"removeTrailingIndexHTML": removeTrailingIndexHTML,
	}
	sections := []*codegen.SectionTemplate{
		codegen.Header(title, "server", []*codegen.ImportSpec{
			{Path: "context"},
			{Path: "fmt"},
			{Path: "io"},
			{Path: "mime/multipart"},
			{Path: "net/http"},
			{Path: "path"},
			{Path: "strings"},
			{Path: "github.com/gorilla/websocket"},
			codegen.GoaImport(""),
			codegen.GoaNamedImport("http", "goahttp"),
			{Path: genpkg + "/" + svcName, Name: data.Service.PkgName},
			{Path: genpkg + "/" + svcName + "/" + "views", Name: data.Service.ViewsPkg},
		}),
	}

	sections = append(sections, &codegen.SectionTemplate{Name: "server-struct", Source: serverStructT, Data: data})
	sections = append(sections, &codegen.SectionTemplate{Name: "server-mountpoint", Source: mountPointStructT, Data: data})

	for _, e := range data.Endpoints {
		if e.MultipartRequestDecoder != nil {
			sections = append(sections, &codegen.SectionTemplate{
				Name:   "multipart-request-decoder-type",
				Source: multipartRequestDecoderTypeT,
				Data:   e.MultipartRequestDecoder,
			})
		}
	}

	sections = append(sections, &codegen.SectionTemplate{Name: "server-init", Source: serverInitT, Data: data, FuncMap: funcs})
	sections = append(sections, &codegen.SectionTemplate{Name: "server-service", Source: serverServiceT, Data: data})
	sections = append(sections, &codegen.SectionTemplate{Name: "server-use", Source: serverUseT, Data: data})
	sections = append(sections, &codegen.SectionTemplate{Name: "server-mount", Source: serverMountT, Data: data, FuncMap: funcs})

	for _, e := range data.Endpoints {
		sections = append(sections, &codegen.SectionTemplate{Name: "server-handler", Source: serverHandlerT, Data: e})
		sections = append(sections, &codegen.SectionTemplate{Name: "server-handler-init", Source: serverHandlerInitT, FuncMap: funcs, Data: e})
	}
	for _, s := range data.FileServers {
		sections = append(sections, &codegen.SectionTemplate{Name: "server-files", Source: fileServerT, FuncMap: funcs, Data: s})
	}

	return &codegen.File{Path: path, SectionTemplates: sections}
}

// serverEncodeDecodeFile returns the file defining the HTTP server encoding and
// decoding logic.
func serverEncodeDecodeFile(genpkg string, svc *expr.HTTPServiceExpr) *codegen.File {
	data := HTTPServices.Get(svc.Name())
	svcName := data.Service.PathName
	path := filepath.Join(codegen.Gendir, "http", svcName, "server", "encode_decode.go")
	title := fmt.Sprintf("%s HTTP server encoders and decoders", svc.Name())
	imports := []*codegen.ImportSpec{
		{Path: "context"},
		{Path: "errors"},
		{Path: "fmt"},
		{Path: "io"},
		{Path: "net/http"},
		{Path: "strconv"},
		{Path: "strings"},
		{Path: "encoding/json"},
		{Path: "mime/multipart"},
		{Path: "unicode/utf8"},
		codegen.GoaImport(""),
		codegen.GoaNamedImport("http", "goahttp"),
		{Path: genpkg + "/" + svcName, Name: data.Service.PkgName},
		{Path: genpkg + "/" + svcName + "/" + "views", Name: data.Service.ViewsPkg},
	}
	imports = append(imports, data.Service.UserTypeImports...)
	sections := []*codegen.SectionTemplate{codegen.Header(title, "server", imports)}

	for _, e := range data.Endpoints {
		if e.Redirect == nil && !isWebSocketEndpoint(e) {
			sections = append(sections, &codegen.SectionTemplate{
				Name:    "response-encoder",
				FuncMap: transTmplFuncs(svc),
				Source:  responseEncoderT,
				Data:    e,
			})
		}
		if mustDecodeRequest(e) {
			fm := transTmplFuncs(svc)
			fm["mapQueryDecodeData"] = mapQueryDecodeData
			sections = append(sections, &codegen.SectionTemplate{
				Name:    "request-decoder",
				Source:  requestDecoderT,
				FuncMap: fm,
				Data:    e,
			})
		}
		if e.MultipartRequestDecoder != nil {
			fm := transTmplFuncs(svc)
			fm["mapQueryDecodeData"] = mapQueryDecodeData
			sections = append(sections, &codegen.SectionTemplate{
				Name:    "multipart-request-decoder",
				Source:  multipartRequestDecoderT,
				FuncMap: fm,
				Data:    e.MultipartRequestDecoder,
			})
		}
		if len(e.Errors) > 0 {
			sections = append(sections, &codegen.SectionTemplate{
				Name:    "error-encoder",
				Source:  errorEncoderT,
				FuncMap: transTmplFuncs(svc),
				Data:    e,
			})
		}
	}
	for _, h := range data.ServerTransformHelpers {
		sections = append(sections, &codegen.SectionTemplate{
			Name:   "server-transform-helper",
			Source: transformHelperT,
			Data:   h,
		})
	}

	// If all endpoints use skip encoding and decoding of both payloads and
	// results and define no error then this file is irrelevant.
	if len(sections) == 1 {
		return nil
	}

	return &codegen.File{Path: path, SectionTemplates: sections}
}

func transTmplFuncs(s *expr.HTTPServiceExpr) map[string]interface{} {
	return map[string]interface{}{
		"goTypeRef": func(dt expr.DataType) string {
			return service.Services.Get(s.Name()).Scope.GoTypeRef(&expr.AttributeExpr{Type: dt})
		},
		"isAliased": func(dt expr.DataType) bool {
			_, ok := dt.(expr.UserType)
			return ok
		},
		"conversionData":       conversionData,
		"headerConversionData": headerConversionData,
		"printValue":           printValue,
		"viewedServerBody":     viewedServerBody,
	}
}

// mustDecodeRequest returns true if the Payload type is not empty.
func mustDecodeRequest(e *EndpointData) bool {
	return e.Payload.Ref != ""
}

// conversionData creates a template context suitable for executing the
// "type_conversion" template.
func conversionData(varName, name string, dt expr.DataType) map[string]interface{} {
	return map[string]interface{}{
		"VarName": varName,
		"Name":    name,
		"Type":    dt,
	}
}

// headerConversionData produces the template data suitable for executing the
// "header_conversion" template.
func headerConversionData(dt expr.DataType, varName string, required bool, target string) map[string]interface{} {
	return map[string]interface{}{
		"Type":     dt,
		"VarName":  varName,
		"Required": required,
		"Target":   target,
	}
}

// printValue generates the Go code for a literal string containing the given
// value. printValue panics if the data type is not a primitive or an array.
func printValue(dt expr.DataType, v interface{}) string {
	switch actual := dt.(type) {
	case *expr.Array:
		val := reflect.ValueOf(v)
		elems := make([]string, val.Len())
		for i := 0; i < val.Len(); i++ {
			elems[i] = printValue(actual.ElemType.Type, val.Index(i).Interface())
		}
		return strings.Join(elems, ", ")
	case expr.Primitive:
		return fmt.Sprintf("%v", v)
	default:
		panic("unsupported type value " + dt.Name()) // bug
	}
}

// viewedServerBody returns the type data that uses the given view for
// rendering.
func viewedServerBody(sbd []*TypeData, view string) *TypeData {
	for _, v := range sbd {
		if v.View == view {
			return v
		}
	}
	panic("view not found in server body types: " + view)
}

func addLeadingSlash(s string) string {
	if strings.HasPrefix(s, "/") {
		return s
	}
	return "/" + s
}

func removeTrailingIndexHTML(s string) string {
	if strings.HasSuffix(s, "/index.html") {
		return strings.TrimSuffix(s, "index.html")
	}
	return s
}

func mapQueryDecodeData(dt expr.DataType, varName string, inc int) map[string]interface{} {
	return map[string]interface{}{
		"Type":      dt,
		"VarName":   varName,
		"Loop":      string(rune(97 + inc)),
		"Increment": inc + 1,
		"Depth":     codegen.MapDepth(expr.AsMap(dt)),
	}
}

// input: ServiceData
const serverStructT = `{{ printf "%s lists the %s service endpoint HTTP handlers." .ServerStruct .Service.Name | comment }}
type {{ .ServerStruct }} struct {
	Mounts []*{{ .MountPointStruct }}
	{{- range .Endpoints }}
	{{ .Method.VarName }} http.Handler
	{{- end }}
	{{- range .FileServers }}
	{{ .VarName }} http.Handler
	{{- end }}
}

// ErrorNamer is an interface implemented by generated error structs that
// exposes the name of the error as defined in the design.
type ErrorNamer interface {
	ErrorName() string
}
`

// input: ServiceData
const mountPointStructT = `{{ printf "%s holds information about the mounted endpoints." .MountPointStruct | comment }}
type {{ .MountPointStruct }} struct {
	{{ printf "Method is the name of the service method served by the mounted HTTP handler." | comment }}
	Method string
	{{ printf "Verb is the HTTP method used to match requests to the mounted handler." | comment }}
	Verb string
	{{ printf "Pattern is the HTTP request path pattern used to match requests to the mounted handler." | comment }}
	Pattern string
}
`

// input: ServiceData
const serverInitT = `{{ printf "%s instantiates HTTP handlers for all the %s service endpoints using the provided encoder and decoder. The handlers are mounted on the given mux using the HTTP verb and path defined in the design. errhandler is called whenever a response fails to be encoded. formatter is used to format errors returned by the service methods prior to encoding. Both errhandler and formatter are optional and can be nil." .ServerInit .Service.Name | comment }}
func {{ .ServerInit }}(
	e *{{ .Service.PkgName }}.Endpoints,
	mux goahttp.Muxer,
	decoder func(*http.Request) goahttp.Decoder,
	encoder func(context.Context, http.ResponseWriter) goahttp.Encoder,
	errhandler func(context.Context, http.ResponseWriter, error),
	formatter func(ctx context.Context, err error) goahttp.Statuser,
	{{- if hasWebSocket . }}
	upgrader goahttp.Upgrader,
	configurer *ConnConfigurer,
	{{- end }}
	{{- range .Endpoints }}
		{{- if .MultipartRequestDecoder }}
	{{ .MultipartRequestDecoder.VarName }} {{ .MultipartRequestDecoder.FuncName }},
		{{- end }}
	{{- end }}
	{{- range .FileServers }}
	{{ .ArgName }} http.FileSystem,
	{{- end }}
) *{{ .ServerStruct }} {
{{- if hasWebSocket . }}
	if configurer == nil {
		configurer = &ConnConfigurer{}
	}
{{- end }}
	{{- range .FileServers }}
	if {{ .ArgName }} == nil {
		{{ .ArgName }} = http.Dir(".")
	}
	{{- end }}
	return &{{ .ServerStruct }}{
		Mounts: []*{{ .MountPointStruct }}{
			{{- range $e := .Endpoints }}
				{{- range $e.Routes }}
			{"{{ $e.Method.VarName }}", "{{ .Verb }}", "{{ .Path }}"},
				{{- end }}
			{{- end }}
			{{- range .FileServers }}
				{{- $filepath := .FilePath }}
				{{- range .RequestPaths }}
			{"{{ $filepath }}", "GET", "{{ . }}"},
				{{- end }}
			{{- end }}
		},
		{{- range .Endpoints }}
		{{ .Method.VarName }}: {{ .HandlerInit }}(e.{{ .Method.VarName }}, mux, {{ if .MultipartRequestDecoder }}{{ .MultipartRequestDecoder.InitName }}(mux, {{ .MultipartRequestDecoder.VarName }}){{ else }}decoder{{ end }}, encoder, errhandler, formatter{{ if isWebSocketEndpoint . }}, upgrader, configurer.{{ .Method.VarName }}Fn{{ end }}),
		{{- end }}
		{{- range .FileServers }}
		{{ .VarName }}: http.FileServer({{ .ArgName }}),
		{{- end }}
	}
}
`

// input: ServiceData
const serverServiceT = `{{ printf "%s returns the name of the service served." .ServerService | comment }}
func (s *{{ .ServerStruct }}) {{ .ServerService }}() string { return "{{ .Service.Name }}" }
`

// input: ServiceData
const serverUseT = `{{ printf "Use wraps the server handlers with the given middleware." | comment }}
func (s *{{ .ServerStruct }}) Use(m func(http.Handler) http.Handler) {
{{- range .Endpoints }}
	s.{{ .Method.VarName }} = m(s.{{ .Method.VarName }})
{{- end }}
}
`

// input: ServiceData
const serverMountT = `{{ printf "%s configures the mux to serve the %s endpoints." .MountServer .Service.Name | comment }}
func {{ .MountServer }}(mux goahttp.Muxer, h *{{ .ServerStruct }}) {
	{{- range .Endpoints }}
	{{ .MountHandler }}(mux, h.{{ .Method.VarName }})
	{{- end }}
	{{- range .FileServers }}
		{{- if .Redirect }}
	{{ .MountHandler }}(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "{{ .Redirect.URL }}", {{ .Redirect.StatusCode }})
		}))
	 	{{- else if .IsDir }}
			{{- $filepath := addLeadingSlash (removeTrailingIndexHTML .FilePath) }}
	{{ .MountHandler }}(mux, {{ range .RequestPaths }}{{if ne . $filepath }}goahttp.Replace("{{ . }}", "{{ $filepath }}", {{ end }}{{ end }}h.{{ .VarName }}){{ range .RequestPaths }}{{ if ne . $filepath }}){{ end}}{{ end }}
		{{- else }}
			{{- $filepath := addLeadingSlash (removeTrailingIndexHTML .FilePath) }}
	{{ .MountHandler }}(mux, {{ range .RequestPaths }}{{if ne . $filepath }}goahttp.Replace("", "{{ $filepath }}", {{ end }}{{ end }}h.{{ .VarName }}){{ range .RequestPaths }}{{ if ne . $filepath }}){{ end}}{{ end }}
		{{- end }}
	{{- end }}
}

{{ printf "%s configures the mux to serve the %s endpoints." .MountServer .Service.Name | comment }}
func (s *{{ .ServerStruct }}) {{ .MountServer }}(mux goahttp.Muxer) {
	{{ .MountServer }}(mux, s)
}
`

// input: EndpointData
const serverHandlerT = `{{ printf "%s configures the mux to serve the %q service %q endpoint." .MountHandler .ServiceName .Method.Name | comment }}
func {{ .MountHandler }}(mux goahttp.Muxer, h http.Handler) {
	f, ok := h.(http.HandlerFunc)
	if !ok {
		f = func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(w, r)
		}
	}
	{{- range .Routes }}
	mux.Handle("{{ .Verb }}", "{{ .Path }}", f)
	{{- end }}
}
`

// input: FileServerData
const fileServerT = `{{ printf "%s configures the mux to serve GET request made to %q." .MountHandler (join .RequestPaths ", ") | comment }}
func {{ .MountHandler }}(mux goahttp.Muxer, h http.Handler) {
	{{- if .IsDir }}
		{{- range .RequestPaths }}
	mux.Handle("GET", "{{ . }}{{if ne . "/"}}/{{end}}", h.ServeHTTP)
	mux.Handle("GET", "{{ . }}{{if ne . "/"}}/{{end}}*{{ $.PathParam }}", h.ServeHTTP)
		{{- end }}
	{{- else }}
		{{- range .RequestPaths }}
	mux.Handle("GET", "{{ . }}", h.ServeHTTP)
		{{- end }}
	{{- end }}
}
`

// input: EndpointData
const serverHandlerInitT = `{{ printf "%s creates a HTTP handler which loads the HTTP request and calls the %q service %q endpoint." .HandlerInit .ServiceName .Method.Name | comment }}
func {{ .HandlerInit }}(
	endpoint goa.Endpoint,
	mux goahttp.Muxer,
	decoder func(*http.Request) goahttp.Decoder,
	encoder func(context.Context, http.ResponseWriter) goahttp.Encoder,
	errhandler func(context.Context, http.ResponseWriter, error),
	formatter func(ctx context.Context, err error) goahttp.Statuser,
	{{- if isWebSocketEndpoint . }}
	upgrader goahttp.Upgrader,
	configurer goahttp.ConnConfigureFunc,
	{{- end }}
) http.Handler {
	{{- if (or (mustDecodeRequest .) (not (or .Redirect (isWebSocketEndpoint .))) (not .Redirect) .Method.SkipResponseBodyEncodeDecode) }}
	var (
	{{- end }}
		{{- if mustDecodeRequest . }}
		decodeRequest  = {{ .RequestDecoder }}(mux, decoder)
		{{- end }}
		{{- if not (or .Redirect (isWebSocketEndpoint .)) }}
		encodeResponse = {{ .ResponseEncoder }}(encoder)
		{{- end }}
		{{- if (or (mustDecodeRequest .) (not .Redirect) .Method.SkipResponseBodyEncodeDecode) }}
		encodeError    = {{ if .Errors }}{{ .ErrorEncoder }}{{ else }}goahttp.ErrorEncoder{{ end }}(encoder, formatter)
		{{- end }}
	{{- if (or (mustDecodeRequest .) (not (or .Redirect (isWebSocketEndpoint .))) (not .Redirect) .Method.SkipResponseBodyEncodeDecode) }}
	)
	{{- end }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), goahttp.AcceptTypeKey, r.Header.Get("Accept"))
		ctx = context.WithValue(ctx, goa.MethodKey, {{ printf "%q" .Method.Name }})
		ctx = context.WithValue(ctx, goa.ServiceKey, {{ printf "%q" .ServiceName }})

	{{- if mustDecodeRequest . }}
		{{ if .Redirect }}_{{ else }}payload{{ end }}, err := decodeRequest(r)
		if err != nil {
			if err := encodeError(ctx, w, err); err != nil {
				errhandler(ctx, w, err)
			}
			return
		}
	{{- else if not .Redirect }}
		var err error
	{{- end }}
	{{- if isWebSocketEndpoint . }}
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		v := &{{ .ServicePkgName }}.{{ .Method.ServerStream.EndpointStruct }}{
			Stream: &{{ .ServerWebSocket.VarName }}{
				upgrader: upgrader,
				configurer: configurer,
				cancel: cancel,
				w: w,
				r: r,
			},
		{{- if .Payload.Ref }}
			Payload: payload.({{ .Payload.Ref }}),
		{{- end }}
		}
		_, err = endpoint(ctx, v)
	{{- else if .Method.SkipRequestBodyEncodeDecode }}
		data := &{{ .ServicePkgName }}.{{ .Method.RequestStruct }}{ {{ if .Payload.Ref }}Payload: payload.({{ .Payload.Ref }}), {{ end }}Body: r.Body }
		res, err := endpoint(ctx, data)
	{{- else if .Redirect }}
		http.Redirect(w, r, "{{ .Redirect.URL }}", {{ .Redirect.StatusCode }})
	{{- else }}
		res, err := endpoint(ctx, {{ if .Payload.Ref }}payload{{ else }}nil{{ end }})
	{{- end }}
	{{- if not .Redirect }}
		if err != nil {
			{{- if isWebSocketEndpoint . }}
			if _, werr := w.Write(nil); werr == http.ErrHijacked {
				// Response writer has been hijacked, do not encode the error
				errhandler(ctx, w, err)
				return
			}
			{{- end }}
			if err := encodeError(ctx, w, err); err != nil {
				errhandler(ctx, w, err)
			}
			return
		}
	{{- end }}
	{{- if .Method.SkipResponseBodyEncodeDecode }}
		o := res.(*{{ .ServicePkgName }}.{{ .Method.ResponseStruct }})
		defer o.Body.Close()
	{{- end }}
	{{- if not (or .Redirect (isWebSocketEndpoint .)) }}
		if err := encodeResponse(ctx, w, {{ if and .Method.SkipResponseBodyEncodeDecode .Result.Ref }}o.Result{{ else }}res{{ end }}); err != nil {
			errhandler(ctx, w, err)
			{{- if .Method.SkipResponseBodyEncodeDecode }}
			return
			{{- end }}
		}
	{{- end }}
	{{- if .Method.SkipResponseBodyEncodeDecode }}
		if _, err := io.Copy(w, o.Body); err != nil {
			if err := encodeError(ctx, w, err); err != nil {
				errhandler(ctx, w, err)
			}
		}
	{{- end }}
	})
}
`

// input: TransformFunctionData
const transformHelperT = `{{ printf "%s builds a value of type %s from a value of type %s." .Name .ResultTypeRef .ParamTypeRef | comment }}
func {{ .Name }}(v {{ .ParamTypeRef }}) {{ .ResultTypeRef }} {
	{{ .Code }}
	return res
}
`

// input: EndpointData
const requestDecoderT = `{{ printf "%s returns a decoder for requests sent to the %s %s endpoint." .RequestDecoder .ServiceName .Method.Name | comment }}
func {{ .RequestDecoder }}(mux goahttp.Muxer, decoder func(*http.Request) goahttp.Decoder) func(*http.Request) (interface{}, error) {
	return func(r *http.Request) (interface{}, error) {
{{- if .MultipartRequestDecoder }}
		var payload {{ .Payload.Ref }}
		if err := decoder(r).Decode(&payload); err != nil {
			return nil, goa.DecodePayloadError(err.Error())
		}
{{- else if .Payload.Request.ServerBody }}
		var (
			body {{ .Payload.Request.ServerBody.VarName }}
			err  error
		)
		err = decoder(r).Decode(&body)
		if err != nil {
	{{- if .Payload.Request.MustHaveBody }}
			if err == io.EOF {
				return nil, goa.MissingPayloadError()
			}
	{{- else }}
			if err == io.EOF {
				err = nil
			} else {
	{{- end }}
			return nil, goa.DecodePayloadError(err.Error())
	{{- if not .Payload.Request.MustHaveBody }}
			}
	{{- end }}
		}
	{{- if .Payload.Request.ServerBody.ValidateRef }}
		{{ .Payload.Request.ServerBody.ValidateRef }}
		if err != nil {
			return nil, err
		}
	{{- end }}
{{- end }}
{{- if not .MultipartRequestDecoder }}
	{{- template "request_elements" .Payload.Request }}
	{{- if .Payload.Request.MustValidate }}
		if err != nil {
			return nil, err
		}
	{{- end }}
	{{- if .Payload.Request.PayloadInit }}
	payload := {{ .Payload.Request.PayloadInit.Name }}({{ range .Payload.Request.PayloadInit.ServerArgs }}{{ .Ref }}, {{ end }})
	{{- else if .Payload.DecoderReturnValue }}
	payload := {{ .Payload.DecoderReturnValue }}
	{{- else }}
	payload := body
	{{- end }}
{{- end }}
{{- if .BasicScheme }}{{ with .BasicScheme }}
	user, pass, {{ if or .UsernameRequired .PasswordRequired }}ok{{ else }}_{{ end }} := r.BasicAuth()
		{{- if or .UsernameRequired .PasswordRequired}}
	if !ok {
		return nil, goa.MissingFieldError("Authorization", "header")
	}
		{{- end }}
	payload.{{ .UsernameField }} = {{ if .UsernamePointer }}&{{ end }}user
	payload.{{ .PasswordField }} = {{ if .PasswordPointer }}&{{ end }}pass
{{- end }}{{ end }}
{{- range .HeaderSchemes }}
	{{- if not .CredRequired }}
	if payload.{{ .CredField }} != nil {
	{{- end }}
	if strings.Contains({{ if .CredPointer }}*{{ end }}payload.{{ .CredField }}, " ") {
		// Remove authorization scheme prefix (e.g. "Bearer")
		cred := strings.SplitN({{ if .CredPointer }}*{{ end }}payload.{{ .CredField }}, " ", 2)[1]
		payload.{{ .CredField }} = {{ if .CredPointer }}&{{ end }}cred
	}
	{{- if not .CredRequired }}
	}
	{{- end }}
{{- end }}

	return payload, nil
	}
}
` + requestElementsT

// input: RequestData
const requestElementsT = `{{- define "request_elements" }}
{{- if or .PathParams .QueryParams .Headers .Cookies }}
{{- if .ServerBody }}{{/* we want a newline only if there was code before */}}
{{ end }}
		var (
		{{- range .PathParams }}
			{{ .VarName }} {{ .TypeRef }}
		{{- end }}
		{{- range .QueryParams }}
			{{ .VarName }} {{ .TypeRef }}
		{{- end }}
		{{- range .Headers }}
			{{ .VarName }} {{ .TypeRef }}
		{{- end }}
		{{- range .Cookies }}
			{{ .VarName }} {{ .TypeRef }}
		{{- end }}
		{{- if and .MustValidate (or (not .ServerBody) .Multipart) }}
			err error
		{{- end }}
		{{- if .Cookies }}
			c *http.Cookie
		{{- end }}
		{{- if .PathParams }}

			params = mux.Vars(r)
		{{- end }}
		)

{{- range .PathParams }}
	{{- if and (or (eq .Type.Name "string") (eq .Type.Name "any")) }}
		{{ .VarName }} = params["{{ .Name }}"]

	{{- else }}{{/* not string and not any */}}
		{
			{{ .VarName }}Raw := params["{{ .Name }}"]
			{{- template "path_conversion" . }}
		}

	{{- end }}
		{{- if .Validate }}
		{{ .Validate }}
		{{- end }}
{{- end }}

{{- range .QueryParams }}
	{{- if and (or (eq .Type.Name "string") (eq .Type.Name "any")) .Required }}
		{{ .VarName }} = r.URL.Query().Get("{{ .Name }}")
		if {{ .VarName }} == "" {
			err = goa.MergeErrors(err, goa.MissingFieldError("{{ .Name }}", "query string"))
		}

	{{- else if (or (eq .Type.Name "string") (eq .Type.Name "any")) }}
		{{ .VarName }}Raw := r.URL.Query().Get("{{ .Name }}")
		if {{ .VarName }}Raw != "" {
			{{ .VarName }} = {{ if and (eq .Type.Name "string") .Pointer }}&{{ end }}{{ .VarName }}Raw
		}
		{{- if .DefaultValue }} else {
			{{ .VarName }} = {{ if eq .Type.Name "string" }}{{ printf "%q" .DefaultValue }}{{ else }}{{ printf "%#v" .DefaultValue }}{{ end }}
		}
		{{- end }}

	{{- else if .StringSlice }}
		{{ .VarName }} = r.URL.Query()["{{ .Name }}"]
		{{- if .Required }}
		if {{ .VarName }} == nil {
			err = goa.MergeErrors(err, goa.MissingFieldError("{{ .Name }}", "query string"))
		}
		{{- else if .DefaultValue }}
		if {{ .VarName }} == nil {
			{{ .VarName }} = []string{
                {{- range $i, $v := .DefaultValue }}
                    {{- if $i }}{{ print ", " }}{{ end }}
                    {{- printf "%q" $v -}} 
                {{- end -}} }
		}
		{{- end }}

	{{- else if .Slice }}
	{
		{{ .VarName }}Raw := r.URL.Query()["{{ .Name }}"]
		{{- if .Required }}
		if {{ .VarName }}Raw == nil {
			return nil, goa.MergeErrors(err, goa.MissingFieldError("{{ .Name }}", "query string"))
		}
		{{- else if .DefaultValue }}
		if {{ .VarName }}Raw == nil {
			{{ .VarName }} = {{ printf "%#v" .DefaultValue }}
		}
		{{- end }}

		{{- if .DefaultValue }}else {
		{{- else if not .Required }}
		if {{ .VarName }}Raw != nil {
		{{- end }}
		{{- template "slice_conversion" . }}
		{{- if or .DefaultValue (not .Required) }}
		}
		{{- end }}
	}

	{{- else if .Map }}
	{
		{{ .VarName }}Raw := r.URL.Query()
		{{- if .Required }}
		if len({{ .VarName }}Raw) == 0 {
			err = goa.MergeErrors(err, goa.MissingFieldError("{{ .Name }}", "query string"))
		}
		{{- else if .DefaultValue }}
		if len({{ .VarName }}Raw) == 0 {
			{{ .VarName }} = {{ printf "%#v" .DefaultValue }}
		}
		{{- end }}

		{{- if .DefaultValue }}else {
		{{- else if not .Required }}
		if len({{ .VarName }}Raw) != 0 {
		{{- end }}
		for keyRaw, valRaw := range {{ .VarName }}Raw {
			if strings.HasPrefix(keyRaw, "{{ .Name }}[") {
				{{- template "map_conversion" (mapQueryDecodeData .Type .VarName 0) }}
			}
		}
		{{- if or .DefaultValue (not .Required) }}
		}
		{{- end }}
	}

	{{- else if .MapQueryParams }}
	{
		{{ .VarName }}Raw := r.URL.Query()
		{{- if .Required }}
		if len({{ .VarName }}Raw) == 0 {
			err = goa.MergeErrors(err, goa.MissingFieldError("{{ .Name }}", "query string"))
		}
		{{- else if .DefaultValue }}
		if len({{ .VarName }}Raw) == 0 {
			{{ .VarName }} = {{ printf "%#v" .DefaultValue }}
		}
		{{- end }}

		{{- if .DefaultValue }}else {
		{{- else if not .Required }}
		if len({{ .VarName }}Raw) != 0 {
		{{- end }}
		for keyRaw, valRaw := range {{ .VarName }}Raw {
			if strings.HasPrefix(keyRaw, "{{ .Name }}[") {
				{{- template "map_conversion" (mapQueryDecodeData .Type .VarName 0) }}
			}
		}
		{{- if or .DefaultValue (not .Required) }}
		}
		{{- end }}
	}

	{{- else }}{{/* not string, not any, not slice and not map */}}
	{
		{{ .VarName }}Raw := r.URL.Query().Get("{{ .Name }}")
		{{- if .Required }}
		if {{ .VarName }}Raw == "" {
			err = goa.MergeErrors(err, goa.MissingFieldError("{{ .Name }}", "query string"))
		}
		{{- else if .DefaultValue }}
		if {{ .VarName }}Raw == "" {
			{{ .VarName }} = {{ printf "%#v" .DefaultValue }}
		}
		{{- end }}

		{{- if .DefaultValue }}else {
		{{- else if not .Required }}
		if {{ .VarName }}Raw != "" {
		{{- end }}
		{{- template "type_conversion" . }}
		{{- if or .DefaultValue (not .Required) }}
		}
		{{- end }}
	}

	{{- end }}
		{{- if .Validate }}
		{{ .Validate }}
		{{- end }}
{{- end }}

{{- range .Headers }}
	{{- if and (or (eq .Type.Name "string") (eq .Type.Name "any")) .Required }}
		{{ .VarName }} = r.Header.Get("{{ .Name }}")
		if {{ .VarName }} == "" {
			err = goa.MergeErrors(err, goa.MissingFieldError("{{ .Name }}", "header"))
		}

	{{- else if (or (eq .Type.Name "string") (eq .Type.Name "any")) }}
		{{ .VarName }}Raw := r.Header.Get("{{ .Name }}")
		if {{ .VarName }}Raw != "" {
			{{ .VarName }} = {{ if and (eq .Type.Name "string") .Pointer }}&{{ end }}{{ .VarName }}Raw
		}
		{{- if .DefaultValue }} else {
			{{ .VarName }} = {{ if eq .Type.Name "string" }}{{ printf "%q" .DefaultValue }}{{ else }}{{ printf "%#v" .DefaultValue }}{{ end }}
		}
		{{- end }}

	{{- else if .StringSlice }}
		{{ .VarName }} = r.Header["{{ .CanonicalName }}"]
		{{- if .Required }}
		if {{ .VarName }} == nil {
			err = goa.MergeErrors(err, goa.MissingFieldError("{{ .Name }}", "header"))
		}
		{{- else if .DefaultValue }}
		if {{ .VarName }} == nil {
			{{ .VarName }} = {{ printf "%#v" .DefaultValue }}
		}
		{{- end }}

	{{- else if .Slice }}
	{
		{{ .VarName }}Raw := r.Header["{{ .CanonicalName }}"]
		{{ if .Required }}if {{ .VarName }}Raw == nil {
			err = goa.MergeErrors(err, goa.MissingFieldError("{{ .Name }}", "header"))
		}
		{{- else if .DefaultValue }}
		if {{ .VarName }}Raw == nil {
			{{ .VarName }} = {{ printf "%#v" .DefaultValue }}
		}
		{{- end }}

		{{- if .DefaultValue }}else {
		{{- else if not .Required }}
		if {{ .VarName }}Raw != nil {
		{{- end }}
		{{- template "slice_conversion" . }}
		{{- if or .DefaultValue (not .Required) }}
		}
		{{- end }}
	}

	{{- else }}{{/* not string, not any and not slice */}}
	{
		{{ .VarName }}Raw := r.Header.Get("{{ .Name }}")
		{{- if .Required }}
		if {{ .VarName }}Raw == "" {
			err = goa.MergeErrors(err, goa.MissingFieldError("{{ .Name }}", "header"))
		}
		{{- else if .DefaultValue }}
		if {{ .VarName }}Raw == "" {
			{{ .VarName }} = {{ printf "%#v" .DefaultValue }}
		}
		{{- end }}

		{{- if .DefaultValue }}else {
		{{- else if not .Required }}
		if {{ .VarName }}Raw != "" {
		{{- end }}
		{{- template "type_conversion" . }}
		{{- if or .DefaultValue (not .Required) }}
		}
		{{- end }}
	}
	{{- end }}
	{{- if .Validate }}
		{{ .Validate }}
	{{- end }}
{{- end }}

{{- range .Cookies }}
	c, {{ if not .Required }}_{{ else }}err{{ end }} = r.Cookie("{{ .Name }}")
	{{- if and (or (eq .Type.Name "string") (eq .Type.Name "any")) .Required }}
		if err == http.ErrNoCookie {
			err = goa.MergeErrors(err, goa.MissingFieldError("{{ .Name }}", "cookie"))
		} else {
			{{ .VarName }} = c.Value
		}

	{{- else if (or (eq .Type.Name "string") (eq .Type.Name "any")) }}
		var {{ .VarName }}Raw string
		if c != nil {
			{{ .VarName }}Raw = c.Value
		}
		if {{ .VarName }}Raw != "" {
			{{ .VarName }} = {{ if and (eq .Type.Name "string") .Pointer }}&{{ end }}{{ .VarName }}Raw
		}
		{{- if .DefaultValue }} else {
			{{ .VarName }} = {{ if eq .Type.Name "string" }}{{ printf "%q" .DefaultValue }}{{ else }}{{ printf "%#v" .DefaultValue }}{{ end }}
		}
		{{- end }}

	{{- else }}{{/* not string and not any */}}
	{
		var {{ .VarName }}Raw string
		if c != nil {
			{{ .VarName }}Raw = c.Value
		}
		{{- if .Required }}
		if {{ .VarName }}Raw == "" {
			err = goa.MergeErrors(err, goa.MissingFieldError("{{ .Name }}", "cookie"))
		}
		{{- else if .DefaultValue }}
		if {{ .VarName }}Raw == "" {
			{{ .VarName }} = {{ printf "%#v" .DefaultValue }}
		}
		{{- end }}

		{{- if .DefaultValue }}else {
		{{- else if not .Required }}
		if {{ .VarName }}Raw != "" {
		{{- end }}
		{{- template "type_conversion" . }}
		{{- if or .DefaultValue (not .Required) }}
		}
		{{- end }}
	}
	{{- end }}
	{{- if .Validate }}
		{{ .Validate }}
	{{- end }}
{{- end }}
{{- end }}
{{- end }}

{{- define "path_conversion" }}
	{{- if eq .Type.Name "array" }}
		{{ .VarName }}RawSlice := strings.Split({{ .VarName }}Raw, ",")
		{{ .VarName }} = make({{ goTypeRef .Type }}, len({{ .VarName }}RawSlice))
		for i, rv := range {{ .VarName }}RawSlice {
			{{- template "slice_item_conversion" . }}
		}
	{{- else }}
		{{- template "type_conversion" . }}
	{{- end }}
{{- end }}

{{- define "map_conversion" }}
	if {{ .VarName }} == nil {
		{{ .VarName }} = make({{ goTypeRef .Type }})
	}
	var key{{ .Loop }} {{ goTypeRef .Type.KeyType.Type }}
	{
		openIdx := strings.IndexRune(keyRaw, '[')
		closeIdx := strings.IndexRune(keyRaw, ']')
	{{- if eq .Type.KeyType.Type.Name "string" }}
		key{{ .Loop }} = keyRaw[openIdx+1 : closeIdx]
	{{- else }}
		key{{ .Loop }}Raw := keyRaw[openIdx+1 : closeIdx]
		{{- template "type_conversion" (conversionData (printf "key%s" .Loop) (printf "%q" "query") .Type.KeyType.Type) }}
	{{- end }}
		{{- if gt .Depth 0 }}
			keyRaw = keyRaw[closeIdx+1:]
		{{- end }}
	}
	{{- if eq .Type.ElemType.Type.Name "string" }}
		{{ .VarName }}[key{{ .Loop }}] = valRaw[0]
	{{- else if eq .Type.ElemType.Type.Name "array" }}
		{{- if eq .Type.ElemType.Type.ElemType.Type.Name "string" }}
			{{ .VarName }}[key{{ .Loop }}] = valRaw
		{{- else }}
			var val {{ goTypeRef .Type.ElemType.Type }}
			{
				{{- template "slice_conversion" (conversionData "val" (printf "%q" "query") .Type.ElemType.Type) }}
			}
			{{ .VarName }}[key{{ .Loop }}] = val
		{{- end }}
	{{- else if eq .Type.ElemType.Type.Name "map" }}
		{{- template "map_conversion" (mapQueryDecodeData .Type.ElemType.Type (printf "%s[key%s]" .VarName .Loop) 1) }}
	{{- else }}
		var val{{ .Loop }} {{ goTypeRef .Type.ElemType.Type }}
		{
			val{{ .Loop }}Raw := valRaw[0]
			{{- template "type_conversion" (conversionData (printf "val%s" .Loop)  (printf "%q" "query") .Type.ElemType.Type) }}
		}
		{{ .VarName }}[key{{ .Loop }}] = val{{ .Loop }}
	{{- end }}
{{- end }}
` + typeConversionT

const typeConversionT = `{{- define "slice_conversion" }}
	{{ .VarName }} = make({{ goTypeRef .Type }}, len({{ .VarName }}Raw))
	for i, rv := range {{ .VarName }}Raw {
		{{- template "slice_item_conversion" . }}
	}
{{- end }}

{{- define "type_conversion" }}
	{{- if eq .Type.Name "bytes" }}
		{{ .VarName }} = []byte({{.VarName}}Raw)
	{{- else if eq .Type.Name "int" }}
		v, err2 := strconv.ParseInt({{ .VarName }}Raw, 10, strconv.IntSize)
		if err2 != nil {
			err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "integer"))
		}
		{{- if .Pointer }}
		pv := int(v)
		{{ .VarName }} = &pv
		{{- else }}
		{{ .VarName }} = int(v)
		{{- end }}
	{{- else if eq .Type.Name "int32" }}
		v, err2 := strconv.ParseInt({{ .VarName }}Raw, 10, 32)
		if err2 != nil {
			err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "integer"))
		}
		{{- if .Pointer }}
		pv := int32(v)
		{{ .VarName }} = &pv
		{{- else }}
		{{ .VarName }} = int32(v)
		{{- end }}
	{{- else if eq .Type.Name "int64" }}
		v, err2 := strconv.ParseInt({{ .VarName }}Raw, 10, 64)
		if err2 != nil {
			err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "integer"))
		}
		{{ .VarName }} = {{ if .Pointer}}&{{ end }}v
	{{- else if eq .Type.Name "uint" }}
		v, err2 := strconv.ParseUint({{ .VarName }}Raw, 10, strconv.IntSize)
		if err2 != nil {
			err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "unsigned integer"))
		}
		{{- if .Pointer }}
		pv := uint(v)
		{{ .VarName }} = &pv
		{{- else }}
		{{ .VarName }} = uint(v)
		{{- end }}
	{{- else if eq .Type.Name "uint32" }}
		v, err2 := strconv.ParseUint({{ .VarName }}Raw, 10, 32)
		if err2 != nil {
			err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "unsigned integer"))
		}
		{{- if .Pointer }}
		pv := uint32(v)
		{{ .VarName }} = &pv
		{{- else }}
		{{ .VarName }} = uint32(v)
		{{- end }}
	{{- else if eq .Type.Name "uint64" }}
		v, err2 := strconv.ParseUint({{ .VarName }}Raw, 10, 64)
		if err2 != nil {
			err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "unsigned integer"))
		}
		{{ .VarName }} = {{ if .Pointer }}&{{ end }}v
	{{- else if eq .Type.Name "float32" }}
		v, err2 := strconv.ParseFloat({{ .VarName }}Raw, 32)
		if err2 != nil {
			err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "float"))
		}
		{{- if .Pointer }}
		pv := float32(v)
		{{ .VarName }} = &pv
		{{- else }}
		{{ .VarName }} = float32(v)
		{{- end }}
	{{- else if eq .Type.Name "float64" }}
		v, err2 := strconv.ParseFloat({{ .VarName }}Raw, 64)
		if err2 != nil {
			err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "float"))
		}
		{{ .VarName }} = {{ if .Pointer }}&{{ end }}v
	{{- else if eq .Type.Name "boolean" }}
		v, err2 := strconv.ParseBool({{ .VarName }}Raw)
		if err2 != nil {
			err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "boolean"))
		}
		{{ .VarName }} = {{ if .Pointer }}&{{ end }}v
	{{- else }}
		// unsupported type {{ .Type.Name }} for var {{ .VarName }}
	{{- end }}
{{- end }}
{{- define "slice_item_conversion" }}
		{{- if eq .Type.ElemType.Type.Name "string" }}
			{{ .VarName }}[i] = rv
		{{- else if eq .Type.ElemType.Type.Name "bytes" }}
			{{ .VarName }}[i] = []byte(rv)
		{{- else if eq .Type.ElemType.Type.Name "int" }}
			v, err2 := strconv.ParseInt(rv, 10, strconv.IntSize)
			if err2 != nil {
				err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "array of integers"))
			}
			{{ .VarName }}[i] = int(v)
		{{- else if eq .Type.ElemType.Type.Name "int32" }}
			v, err2 := strconv.ParseInt(rv, 10, 32)
			if err2 != nil {
				err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "array of integers"))
			}
			{{ .VarName }}[i] = int32(v)
		{{- else if eq .Type.ElemType.Type.Name "int64" }}
			v, err2 := strconv.ParseInt(rv, 10, 64)
			if err2 != nil {
				err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "array of integers"))
			}
			{{ .VarName }}[i] = v
		{{- else if eq .Type.ElemType.Type.Name "uint" }}
			v, err2 := strconv.ParseUint(rv, 10, strconv.IntSize)
			if err2 != nil {
				err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "array of unsigned integers"))
			}
			{{ .VarName }}[i] = uint(v)
		{{- else if eq .Type.ElemType.Type.Name "uint32" }}
			v, err2 := strconv.ParseUint(rv, 10, 32)
			if err2 != nil {
				err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "array of unsigned integers"))
			}
			{{ .VarName }}[i] = uint32(v)
		{{- else if eq .Type.ElemType.Type.Name "uint64" }}
			v, err2 := strconv.ParseUint(rv, 10, 64)
			if err2 != nil {
				err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "array of unsigned integers"))
			}
			{{ .VarName }}[i] = v
		{{- else if eq .Type.ElemType.Type.Name "float32" }}
			v, err2 := strconv.ParseFloat(rv, 32)
			if err2 != nil {
				err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "array of floats"))
			}
			{{ .VarName }}[i] = float32(v)
		{{- else if eq .Type.ElemType.Type.Name "float64" }}
			v, err2 := strconv.ParseFloat(rv, 64)
			if err2 != nil {
				err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "array of floats"))
			}
			{{ .VarName }}[i] = v
		{{- else if eq .Type.ElemType.Type.Name "boolean" }}
			v, err2 := strconv.ParseBool(rv)
			if err2 != nil {
				err = goa.MergeErrors(err, goa.InvalidFieldTypeError({{ printf "%q" .VarName }}, {{ .VarName}}Raw, "array of booleans"))
			}
			{{ .VarName }}[i] = v
		{{- else if eq .Type.ElemType.Type.Name "any" }}
			{{ .VarName }}[i] = rv
		{{- else }}
			// unsupported slice type {{ .Type.ElemType.Type.Name }} for var {{ .VarName }}
		{{- end }}
{{- end }}
`

// input: EndpointData
const responseEncoderT = `{{ printf "%s returns an encoder for responses returned by the %s %s endpoint." .ResponseEncoder .ServiceName .Method.Name | comment }}
func {{ .ResponseEncoder }}(encoder func(context.Context, http.ResponseWriter) goahttp.Encoder) func(context.Context, http.ResponseWriter, interface{}) error {
	return func(ctx context.Context, w http.ResponseWriter, v interface{}) error {
	{{- if .Result.MustInit }}
		{{- if .Method.ViewedResult }}
			res := v.({{ .Method.ViewedResult.FullRef }})
			{{- if not .Method.ViewedResult.ViewName }}
				w.Header().Set("goa-view", res.View)
			{{- end }}
		{{- else }}
			res, _ := v.({{ .Result.Ref }})
		{{- end }}
		{{- range .Result.Responses }}
			{{- if .ContentType }}
				ctx = context.WithValue(ctx, goahttp.ContentTypeKey, "{{ .ContentType }}")
			{{- end }}
			{{- if .TagName }}
				{{- if .TagPointer }}
					if res.{{ if .ViewedResult }}Projected.{{ end }}{{ .TagName }} != nil && *res.{{ if .ViewedResult }}Projected.{{ end }}{{ .TagName }} == {{ printf "%q" .TagValue }} {
				{{- else }}
					if {{ if .ViewedResult }}*{{ end }}res.{{ if .ViewedResult }}Projected.{{ end }}{{ .TagName }} == {{ printf "%q" .TagValue }} {
				{{- end }}
			{{- end -}}
			{{ template "response" . }}
			{{- if .ServerBody }}
				return enc.Encode(body)
			{{- else }}
				return nil
			{{- end }}
			{{- if .TagName }}
				}
			{{- end }}
		{{- end }}
	{{- else }}
		{{- with (index .Result.Responses 0) }}
			w.WriteHeader({{ .StatusCode }})
			return nil
		{{- end }}
	{{- end }}
	}
}
` + responseT

// input: EndpointData
const errorEncoderT = `{{ printf "%s returns an encoder for errors returned by the %s %s endpoint." .ErrorEncoder .Method.Name .ServiceName | comment }}
func {{ .ErrorEncoder }}(encoder func(context.Context, http.ResponseWriter) goahttp.Encoder, formatter func(ctx context.Context, err error) goahttp.Statuser) func(context.Context, http.ResponseWriter, error) error {
	encodeError := goahttp.ErrorEncoder(encoder, formatter)
	return func(ctx context.Context, w http.ResponseWriter, v error) error {
		var en ErrorNamer
		if !errors.As(v, &en) {
			return encodeError(ctx, w, v)
		}
		switch en.ErrorName() {
	{{- range $gerr := .Errors }}
	{{- range $err := .Errors }}
		case {{ printf "%q" .Name }}:
			var res {{ $err.Ref }}
			errors.As(v, &res)
			{{- with .Response}}
				{{- if .ContentType }}
					ctx = context.WithValue(ctx, goahttp.ContentTypeKey, "{{ .ContentType }}")
				{{- end }}
				{{- template "response" . }}
				{{- if .ServerBody }}
				return enc.Encode(body)
				{{- else }}
				return nil
				{{- end }}
			{{- end }}
	{{- end }}
	{{- end }}
		default:
			return encodeError(ctx, w, v)
		}
	}
}
` + responseT

// input: ResponseData
const responseT = `{{ define "response" -}}
	{{- $servBodyLen := len .ServerBody }}
	{{- if gt $servBodyLen 0 }}
	enc := encoder(ctx, w)
	{{- end }}
	{{- if gt $servBodyLen 0 }}
		{{- if and (gt $servBodyLen 1) $.ViewedResult }}
	var body interface{}
	switch res.View	{
			{{- range $.ViewedResult.Views }}
	case {{ printf "%q" .Name }}{{ if eq .Name "default" }}, ""{{ end }}:
		{{- $vsb := (viewedServerBody $.ServerBody .Name) }}
		body = {{ $vsb.Init.Name }}({{ range $vsb.Init.ServerArgs }}{{ .Ref }}, {{ end }})
			{{- end }}
	}
		{{- else if (index .ServerBody 0).Init }}
			{{- if .ErrorHeader }}
	var body interface{}
	if formatter != nil {
		body = formatter(ctx, {{ (index (index .ServerBody 0).Init.ServerArgs 0).Ref }})
	} else {
			{{- end }}
	body {{ if not .ErrorHeader}}:{{ end }}= {{ (index .ServerBody 0).Init.Name }}({{ range (index .ServerBody 0).Init.ServerArgs }}{{ .Ref }}, {{ end }})
			{{- if .ErrorHeader }}
	}
			{{- end }}
		{{- else }}
	body := res{{ if $.ViewedResult }}.Projected{{ end }}{{ if .ResultAttr }}.{{ .ResultAttr }}{{ end }}
		{{- end }}
	{{- end }}
	{{- range .Headers }}
		{{- $initDef := and (or .FieldPointer .Slice) .DefaultValue (not $.TagName) }}
		{{- $checkNil := and (or .FieldPointer .Slice (eq .Type.Name "bytes") (eq .Type.Name "any") $initDef) (not $.TagName) }}
		{{- if $checkNil }}
	if res{{ if .FieldName }}.{{ end }}{{ if $.ViewedResult }}Projected.{{ end }}{{ if .FieldName }}{{ .FieldName }}{{ end }} != nil {
		{{- end }}

		{{- if and (eq .Type.Name "string") (not (isAliased .FieldType)) }}
	w.Header().Set("{{ .CanonicalName }}", {{ if or .FieldPointer $.ViewedResult }}*{{ end }}res{{ if $.ViewedResult }}.Projected{{ end }}{{ if .FieldName }}.{{ .FieldName }}{{ end }})
		{{- else }}
{{- if not $checkNil }}
{
{{- end }}
			{{- if isAliased .FieldType }}
	val := {{ goTypeRef .Type }}({{ if .FieldPointer }}*{{ end }}res{{ if $.ViewedResult }}.Projected{{ end }}{{ if .FieldName }}.{{ .FieldName }}{{ end }})
	{{ template "header_conversion" (headerConversionData .Type (printf "%ss" .VarName) true "val") }}
			{{- else }}
	val := res{{ if $.ViewedResult }}.Projected{{ end }}{{ if .FieldName }}.{{ .FieldName }}{{ end }}
	{{ template "header_conversion" (headerConversionData .Type (printf "%ss" .VarName) (not .FieldPointer) "val") }}
			{{- end }}
	w.Header().Set("{{ .CanonicalName }}", {{ .VarName }}s)
{{- if not $checkNil }}
}
{{- end }}
		{{- end }}

		{{- if $initDef }}
	{{ if $checkNil }} } else { {{ else }}if res{{ if $.ViewedResult }}.Projected{{ end }}.{{ .FieldName }} == nil { {{ end }}
		w.Header().Set("{{ .CanonicalName }}", "{{ printValue .Type .DefaultValue }}")
		{{- end }}

		{{- if or $checkNil $initDef }}
	}
		{{- end }}

	{{- end }}

	{{- range .Cookies }}
		{{- $initDef := and (or .FieldPointer .Slice) .DefaultValue }}
		{{- $checkNil := and (or .FieldPointer .Slice (eq .Type.Name "bytes") (eq .Type.Name "any") $initDef) }}
		{{- if $checkNil }}
	if res.{{ if $.ViewedResult }}Projected.{{ end }}{{ .FieldName }} != nil {
		{{- end }}

		{{- if eq .Type.Name "string" }}
	{{ .VarName }} := {{ if or .FieldPointer $.ViewedResult }}*{{ end }}res{{ if $.ViewedResult }}.Projected{{ end }}{{ if .FieldName }}.{{ .FieldName }}{{ end }}
		{{- else }}
			{{- if isAliased .FieldType }}
	{{ .VarName }}raw := {{ goTypeRef .Type }}({{ if .FieldPointer }}*{{ end }}res{{ if $.ViewedResult }}.Projected{{ end }}{{ if .FieldName }}.{{ .FieldName }}{{ end }})
	{{ template "header_conversion" (headerConversionData .Type (printf "%sraw" .VarName) true .VarName) }}
			{{- else }}
	{{ .VarName }}raw := res{{ if $.ViewedResult }}.Projected{{ end }}{{ if .FieldName }}.{{ .FieldName }}{{ end }}
	{{ template "header_conversion" (headerConversionData .Type (printf "%sraw" .VarName) (not .FieldPointer) .VarName) }}
			{{- end }}
		{{- end }}

		{{- if $initDef }}
	{{ if $checkNil }} } else { {{ else }}if res{{ if $.ViewedResult }}.Projected{{ end }}.{{ .FieldName }} == nil { {{ end }}
		{{ .VarName }} := "{{ printValue .Type .DefaultValue }}"
		{{- end }}
		http.SetCookie(w, &http.Cookie{
			Name: {{ printf "%q" .Name }},
			Value: {{ .VarName }},
			{{- if .MaxAge }}
			MaxAge: {{ .MaxAge }},
			{{- end }}
			{{- if .Path }}
			Path: {{ printf "%q" .Path }},
			{{- end }}
			{{- if .Domain }}
			Domain: {{ printf "%q" .Domain }},
			{{- end }}
			{{- if .Secure }}
			Secure: true,
			{{- end }}
			{{- if .HTTPOnly }}
			HttpOnly: true,
			{{- end }}
		})
		{{- if or $checkNil $initDef }}
	}
		{{- end }}

	{{- end }}

	{{- if .ErrorHeader }}
	w.Header().Set("goa-error", res.ErrorName())
	{{- end }}
	w.WriteHeader({{ .StatusCode }})
{{- end }}

{{- define "header_conversion" }}
	{{- if eq .Type.Name "boolean" -}}
		{{ .VarName }} := strconv.FormatBool({{ if not .Required }}*{{ end }}{{ .Target }})
	{{- else if eq .Type.Name "int" -}}
		{{ .VarName }} := strconv.Itoa({{ if not .Required }}*{{ end }}{{ .Target }})
	{{- else if eq .Type.Name "int32" -}}
		{{ .VarName }} := strconv.FormatInt(int64({{ if not .Required }}*{{ end }}{{ .Target }}), 10)
	{{- else if eq .Type.Name "int64" -}}
		{{ .VarName }} := strconv.FormatInt({{ if not .Required }}*{{ end }}{{ .Target }}, 10)
	{{- else if eq .Type.Name "uint" -}}
		{{ .VarName }} := strconv.FormatUint(uint64({{ if not .Required }}*{{ end }}{{ .Target }}), 10)
	{{- else if eq .Type.Name "uint32" -}}
		{{ .VarName }} := strconv.FormatUint(uint64({{ if not .Required }}*{{ end }}{{ .Target }}), 10)
	{{- else if eq .Type.Name "uint64" -}}
		{{ .VarName }} := strconv.FormatUint({{ if not .Required }}*{{ end }}{{ .Target }}, 10)
	{{- else if eq .Type.Name "float32" -}}
		{{ .VarName }} := strconv.FormatFloat(float64({{ if not .Required }}*{{ end }}{{ .Target }}), 'f', -1, 32)
	{{- else if eq .Type.Name "float64" -}}
		{{ .VarName }} := strconv.FormatFloat({{ if not .Required }}*{{ end }}{{ .Target }}, 'f', -1, 64)
	{{- else if eq .Type.Name "string" -}}
		{{ .VarName }} := {{ .Target }}
	{{- else if eq .Type.Name "bytes" -}}
		{{ .VarName }} := string({{ .Target }})
	{{- else if eq .Type.Name "any" -}}
		{{ .VarName }} := fmt.Sprintf("%v", {{ .Target }})
	{{- else if eq .Type.Name "array" -}}
		{{- if eq .Type.ElemType.Type.Name "string" -}}
		{{ .VarName }} := strings.Join({{ .Target }}, ", ")
		{{- else -}}
		{{ .VarName }}Slice := make([]string, len({{ .Target }}))
		for i, e := range {{ .Target }}  {
			{{ template "header_conversion" (headerConversionData .Type.ElemType.Type "es" true "e") }}
			{{ .VarName }}Slice[i] = es
		}
		{{ .VarName }} := strings.Join({{ .VarName }}Slice, ", ")
		{{- end }}
	{{- else }}
		// unsupported type {{ .Type.Name }} for header field {{ .FieldName }}
	{{- end }}
{{- end -}}
`

// input: multipartData
const multipartRequestDecoderTypeT = `{{ printf "%s is the type to decode multipart request for the %q service %q endpoint." .FuncName .ServiceName .MethodName | comment }}
type {{ .FuncName }} func(*multipart.Reader, *{{ .Payload.Ref }}) error
`

// input: multipartData
const multipartRequestDecoderT = `{{ printf "%s returns a decoder to decode the multipart request for the %q service %q endpoint." .InitName .ServiceName .MethodName | comment }}
func {{ .InitName }}(mux goahttp.Muxer, {{ .VarName }} {{ .FuncName }}) func(r *http.Request) goahttp.Decoder {
	return func(r *http.Request) goahttp.Decoder {
		return goahttp.EncodingFunc(func(v interface{}) error {
			mr, merr := r.MultipartReader()
			if merr != nil {
				return merr
			}
			p := v.(*{{ .Payload.Ref }})
			if err := {{ .VarName }}(mr, p); err != nil {
				return err
			}
			{{- template "request_elements" .Payload.Request }}
			{{- if .Payload.Request.MustValidate }}
			if err != nil {
				return err
			}
			{{- end }}
			{{- if .Payload.Request.PayloadInit }}
				{{- range .Payload.Request.PayloadInit.ServerArgs }}
					{{- if .FieldName }}
			(*p).{{ .FieldName }} = {{ if and (not .Pointer) .FieldPointer }}&{{ end }}{{ .VarName }}
					{{- end }}
				{{- end }}
			{{- end }}
			return nil
		})
	}
}
` + requestElementsT
