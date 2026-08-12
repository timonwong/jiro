package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/timonwong/jiro/internal/apperr"
	"github.com/timonwong/jiro/internal/jira"
)

const rawHTTPOutputAnnotation = "jiro.io/raw-http-output"

var apiMethodSuggestions = []string{
	http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
}

var apiReadOnlyMethodNames = []string{http.MethodGet, http.MethodHead, http.MethodOptions}

var apiReadOnlyMethods = apiMethodSet(apiReadOnlyMethodNames)

var apiProtectedHeaders = map[string]struct{}{
	"authorization": {}, "proxy-authorization": {}, "host": {}, "content-length": {}, "user-agent": {},
}

type apiFieldKind uint8

const (
	apiStringField apiFieldKind = iota
	apiTypedField
)

type apiFieldArgument struct {
	kind apiFieldKind
	raw  string
}

type apiFieldFlag struct {
	fields *[]apiFieldArgument
	kind   apiFieldKind
}

func (f apiFieldFlag) String() string { return "" }
func (f apiFieldFlag) Type() string   { return "key=value" }
func (f apiFieldFlag) Set(value string) error {
	*f.fields = append(*f.fields, apiFieldArgument{kind: f.kind, raw: value})
	return nil
}

type apiOptions struct {
	method   string
	input    string
	fields   []apiFieldArgument
	forms    []string
	headers  []string
	timeout  time.Duration
	insecure bool
	include  bool
}

type apiPreparedRequest struct {
	method        string
	endpoint      string
	header        http.Header
	body          io.Reader
	contentLength int64
	query         []jira.QueryParameter
	closers       []io.Closer
}

func (r *apiPreparedRequest) close() {
	for index := len(r.closers) - 1; index >= 0; index-- {
		_ = r.closers[index].Close()
	}
}

type apiRenderedError struct{ error }

func (e *apiRenderedError) Unwrap() error { return e.error }

func (a *app) apiCommand() *cobra.Command {
	var options apiOptions
	command := &cobra.Command{
		Use:   "api ENDPOINT",
		Short: "Send an authenticated raw HTTP request to Jira",
		Long: "Send one authenticated HTTP request to a relative endpoint of the selected Jira Instance.\n" +
			"Responses are streamed as Jira-owned raw bytes; global --output is not supported.\n" +
			"Using --insecure disables TLS certificate and hostname verification and is unsafe.",
		Example:     "  jiro api rest/api/2/myself\n  jiro api rest/api/2/issue -F fields='{\"summary\":\"Example\"}'\n  jiro api plugins/servlet/example --include",
		Args:        exactArgs(1),
		Annotations: map[string]string{rawHTTPOutputAnnotation: "true"},
		RunE: func(command *cobra.Command, args []string) error {
			prepared, err := a.prepareAPIRequest(command, args[0], options)
			if err != nil {
				return err
			}
			defer prepared.close()

			preview, err := a.previewSettings()
			if err != nil {
				return err
			}
			if preview.ReadOnly {
				if _, ok := apiReadOnlyMethods[prepared.method]; !ok {
					return apperr.New(apperr.KindInvalidInput, "HTTP method "+prepared.method+" blocked by read_only configuration")
				}
			}

			settings, err := a.settings()
			if err != nil {
				return err
			}
			client, err := clientFromSettings(settings)
			if err != nil {
				return err
			}

			response, err := client.Stream(command.Context(), jira.StreamRequest{
				Method: prepared.method, Endpoint: prepared.endpoint, Query: prepared.query, Header: prepared.header,
				Body: prepared.body, ContentLength: prepared.contentLength, Timeout: options.timeout,
				Insecure: options.insecure,
			})
			if err != nil {
				return err
			}
			defer response.Body.Close()
			return a.renderAPIResponse(response, options.include)
		},
	}

	flags := command.Flags()
	flags.StringVarP(&options.method, "method", "X", "", "HTTP method")
	flags.StringVar(&options.input, "input", "", "stream request body from file or - for stdin")
	flags.VarP(apiFieldFlag{fields: &options.fields, kind: apiStringField}, "string-field", "f", "string field as key=value; repeatable")
	flags.VarP(apiFieldFlag{fields: &options.fields, kind: apiTypedField}, "field", "F", "JSON-or-string field as key=value; repeatable")
	flags.StringArrayVar(&options.forms, "form", nil, "multipart form field as key=value; repeatable")
	flags.StringArrayVarP(&options.headers, "header", "H", nil, "request header as 'Name: value'; repeatable")
	flags.DurationVar(&options.timeout, "timeout", 30*time.Second, "request timeout; 0 disables the timeout")
	flags.BoolVar(&options.insecure, "insecure", false, "disable TLS certificate and hostname verification for this request (unsafe)")
	flags.BoolVarP(&options.include, "include", "i", false, "include final response status and headers")
	return command
}

func (a *app) prepareAPIRequest(command *cobra.Command, endpoint string, options apiOptions) (*apiPreparedRequest, error) {
	if err := jira.ValidateRelativeEndpoint(endpoint); err != nil {
		return nil, err
	}
	if options.timeout < 0 {
		return nil, apperr.New(apperr.KindInvalidInput, "timeout must not be negative")
	}
	if len(options.forms) > 0 && (options.input != "" || len(options.fields) > 0) {
		return nil, apperr.New(apperr.KindInvalidInput, "--form is mutually exclusive with --input, --field, and --string-field")
	}
	if usesAPIStdin(options) > 1 {
		return nil, apperr.New(apperr.KindInvalidInput, "stdin can be consumed only once")
	}

	explicitMethod := command.Flags().Lookup("method").Changed
	inputSet := command.Flags().Lookup("input").Changed
	if inputSet && options.input == "" {
		return nil, apperr.New(apperr.KindInvalidInput, "--input requires a file path or -")
	}
	method := strings.ToUpper(strings.TrimSpace(options.method))
	if explicitMethod && method == "" {
		return nil, apperr.New(apperr.KindInvalidInput, "--method must not be empty")
	}
	if !explicitMethod {
		method = http.MethodGet
		if inputSet || len(options.fields) > 0 || len(options.forms) > 0 {
			method = http.MethodPost
		}
	}
	bodylessMethod := method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions

	queryFields := len(options.fields) > 0 && (inputSet || (explicitMethod && bodylessMethod))
	parsedFields, err := a.parseAPIFields(command.Context(), options.fields)
	if err != nil {
		return nil, err
	}
	prepared := &apiPreparedRequest{method: method, endpoint: endpoint, contentLength: 0}
	if queryFields {
		prepared.query = apiQueryFields(parsedFields)
	}
	hasBody := false
	formContentType := ""
	switch {
	case inputSet:
		body, length, closer, openErr := a.openAPIInput(command.Context(), options.input)
		if openErr != nil {
			return nil, openErr
		}
		prepared.body = body
		prepared.contentLength = length
		if closer != nil {
			prepared.closers = append(prepared.closers, closer)
		}
		hasBody = true
	case len(options.forms) > 0:
		body, length, contentType, closers, formErr := a.prepareAPIMultipart(command.Context(), options.forms)
		if formErr != nil {
			return nil, formErr
		}
		prepared.body = body
		prepared.contentLength = length
		prepared.closers = append(prepared.closers, closers...)
		formContentType = contentType
		hasBody = true
	case len(parsedFields) > 0 && !queryFields:
		body, encodeErr := encodeAPIFieldObject(parsedFields)
		if encodeErr != nil {
			return nil, encodeErr
		}
		prepared.body = bytes.NewReader(body)
		prepared.contentLength = int64(len(body))
		hasBody = true
	}

	prepared.header, err = parseAPIHeaders(options.headers, hasBody, len(options.forms) > 0)
	if err != nil {
		prepared.close()
		return nil, err
	}
	if formContentType != "" {
		prepared.header.Set("Content-Type", formContentType)
	}
	return prepared, nil
}

func apiMethodSet(methods []string) map[string]struct{} {
	result := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		result[method] = struct{}{}
	}
	return result
}

func isRawHTTPCommand(command *cobra.Command) bool {
	return command != nil && command.Annotations[rawHTTPOutputAnnotation] == "true"
}

type parsedAPIField struct {
	key   string
	value any
	kind  apiFieldKind
}

func (a *app) parseAPIFields(ctx context.Context, arguments []apiFieldArgument) ([]parsedAPIField, error) {
	fields := make([]parsedAPIField, 0, len(arguments))
	for _, argument := range arguments {
		key, raw, found := strings.Cut(argument.raw, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return nil, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("field %q must be key=value", argument.raw))
		}
		if argument.kind == apiStringField {
			fields = append(fields, parsedAPIField{key: key, value: raw, kind: argument.kind})
			continue
		}

		data := []byte(raw)
		if strings.HasPrefix(raw, "@") {
			var err error
			data, err = a.readAPIFieldSource(ctx, strings.TrimPrefix(raw, "@"))
			if err != nil {
				return nil, err
			}
		}
		value, decoded := decodeAPIFieldValue(data)
		if !decoded {
			value = string(data)
		}
		fields = append(fields, parsedAPIField{key: key, value: value, kind: argument.kind})
	}
	return fields, nil
}

func (a *app) readAPIFieldSource(ctx context.Context, path string) ([]byte, error) {
	var reader io.Reader
	var ownedCloser io.Closer
	var cancelCloser io.Closer
	if path == "-" {
		reader = a.stdin
		cancelCloser, _ = a.stdin.(io.Closer)
	} else {
		file, err := openAPIFileWithContext(ctx, path)
		if err != nil {
			return nil, apperr.Wrap(apperr.KindInvalidInput, err, "read API field file %q", path)
		}
		reader, ownedCloser, cancelCloser = file, file, file
	}
	if ownedCloser != nil {
		defer ownedCloser.Close()
	}
	data, err := readAPIInputWithContext(ctx, reader, cancelCloser)
	if err != nil {
		return nil, apperr.Wrap(apperr.KindInvalidInput, err, "read API field input")
	}
	return data, nil
}

type apiReadResult struct {
	data []byte
	err  error
}

func readAPIInputWithContext(ctx context.Context, reader io.Reader, closer io.Closer) ([]byte, error) {
	result := make(chan apiReadResult, 1)
	go func() {
		data, err := io.ReadAll(reader)
		result <- apiReadResult{data: data, err: err}
	}()
	select {
	case completed := <-result:
		return completed.data, completed.err
	case <-ctx.Done():
		if closer != nil {
			_ = closer.Close()
		}
		return nil, ctx.Err()
	}
}

func decodeAPIFieldValue(data []byte) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, false
	}
	return value, true
}

func encodeAPIFieldObject(fields []parsedAPIField) ([]byte, error) {
	object := make(map[string]any, len(fields))
	for _, field := range fields {
		object[field.key] = field.value
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, apperr.Wrap(apperr.KindInvalidInput, err, "encode API fields")
	}
	return encoded, nil
}

func apiQueryFields(fields []parsedAPIField) []jira.QueryParameter {
	result := make([]jira.QueryParameter, 0, len(fields))
	for _, field := range fields {
		value := fmt.Sprint(field.value)
		if field.kind == apiTypedField {
			if text, ok := field.value.(string); ok {
				value = text
			} else if encoded, err := json.Marshal(field.value); err == nil {
				value = string(encoded)
			}
		}
		result = append(result, jira.QueryParameter{Key: field.key, Value: value})
	}
	return result
}

func (a *app) openAPIInput(ctx context.Context, path string) (io.Reader, int64, io.Closer, error) {
	if path == "-" {
		body := newAPIContextReader(ctx, a.stdin)
		return body, -1, body, nil
	}
	file, err := openAPIFileWithContext(ctx, path)
	if err != nil {
		return nil, 0, nil, apperr.Wrap(apperr.KindInvalidInput, err, "open API input %q", path)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, nil, apperr.Wrap(apperr.KindInvalidInput, err, "inspect API input %q", path)
	}
	if info.IsDir() {
		_ = file.Close()
		return nil, 0, nil, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("API input %q is a directory", path))
	}
	length := int64(-1)
	if info.Mode().IsRegular() {
		length = info.Size()
	}
	return file, length, file, nil
}

type apiOpenResult struct {
	file *os.File
	err  error
}

func openAPIFileWithContext(ctx context.Context, path string) (*os.File, error) {
	result := make(chan apiOpenResult, 1)
	go func() {
		file, err := os.Open(path)
		result <- apiOpenResult{file: file, err: err}
	}()
	select {
	case completed := <-result:
		return completed.file, completed.err
	case <-ctx.Done():
		go func() {
			completed := <-result
			if completed.file != nil {
				_ = completed.file.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

type apiReadCall struct {
	data []byte
	err  error
}

type apiContextReader struct {
	ctx    context.Context
	reader io.Reader
	closer io.Closer
	once   sync.Once
}

func newAPIContextReader(ctx context.Context, reader io.Reader) *apiContextReader {
	closer, _ := reader.(io.Closer)
	return &apiContextReader{ctx: ctx, reader: reader, closer: closer}
}

func (r *apiContextReader) Read(value []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
	}
	result := make(chan apiReadCall, 1)
	go func() {
		buffer := make([]byte, len(value))
		count, err := r.reader.Read(buffer)
		result <- apiReadCall{data: buffer[:count], err: err}
	}()
	select {
	case completed := <-result:
		return copy(value, completed.data), completed.err
	case <-r.ctx.Done():
		_ = r.Close()
		return 0, r.ctx.Err()
	}
}

func (r *apiContextReader) Close() error {
	var err error
	r.once.Do(func() {
		if r.closer != nil {
			err = r.closer.Close()
		}
	})
	return err
}

type apiFormPart struct {
	name        string
	reader      io.Reader
	size        int64
	filename    string
	contentType string
}

func (a *app) prepareAPIMultipart(ctx context.Context, values []string) (io.Reader, int64, string, []io.Closer, error) {
	parts := make([]apiFormPart, 0, len(values))
	closers := make([]io.Closer, 0, len(values))
	closeAll := func() {
		for _, closer := range closers {
			_ = closer.Close()
		}
	}
	for _, value := range values {
		name, raw, found := strings.Cut(value, "=")
		name = strings.TrimSpace(name)
		if !found || name == "" {
			closeAll()
			return nil, 0, "", nil, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("form field %q must be key=value", value))
		}
		if strings.HasPrefix(raw, "@@") {
			text := strings.TrimPrefix(raw, "@")
			parts = append(parts, apiFormPart{name: name, reader: strings.NewReader(text), size: int64(len(text))})
			continue
		}
		if !strings.HasPrefix(raw, "@") {
			parts = append(parts, apiFormPart{name: name, reader: strings.NewReader(raw), size: int64(len(raw))})
			continue
		}

		path := strings.TrimPrefix(raw, "@")
		if path == "-" {
			parts = append(parts, apiFormPart{
				name: name, reader: a.stdin, size: -1, filename: "stdin", contentType: "application/octet-stream",
			})
			if closer, ok := a.stdin.(io.Closer); ok {
				closers = append(closers, closer)
			}
			continue
		}
		file, err := openAPIFileWithContext(ctx, path)
		if err != nil {
			closeAll()
			return nil, 0, "", nil, apperr.Wrap(apperr.KindInvalidInput, err, "open API form file %q", path)
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			closeAll()
			return nil, 0, "", nil, apperr.Wrap(apperr.KindInvalidInput, err, "inspect API form file %q", path)
		}
		if info.IsDir() {
			_ = file.Close()
			closeAll()
			return nil, 0, "", nil, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("API form file %q is a directory", path))
		}
		mediaType := mime.TypeByExtension(filepath.Ext(path))
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		size := int64(-1)
		if info.Mode().IsRegular() {
			size = info.Size()
		}
		filename := filepath.Base(path)
		parts = append(parts, apiFormPart{
			name: name, reader: file, size: size, filename: filename, contentType: mediaType,
		})
		closers = append(closers, file)
	}

	probe := multipart.NewWriter(io.Discard)
	boundary := probe.Boundary()
	contentType := probe.FormDataContentType()
	_ = probe.Close()
	length, err := apiMultipartLength(boundary, parts)
	if err != nil {
		closeAll()
		return nil, 0, "", nil, err
	}
	body := newAPIMultipartReader(boundary, parts)
	closers = append(closers, body)
	return body, length, contentType, closers, nil
}

func apiMultipartLength(boundary string, parts []apiFormPart) (int64, error) {
	for _, part := range parts {
		if part.size < 0 {
			return -1, nil
		}
	}
	counter := &apiCountingWriter{}
	writer := multipart.NewWriter(counter)
	if err := writer.SetBoundary(boundary); err != nil {
		return 0, apperr.Wrap(apperr.KindUnexpected, err, "set multipart boundary")
	}
	for _, part := range parts {
		if _, err := writer.CreatePart(apiMultipartHeader(part)); err != nil {
			return 0, apperr.Wrap(apperr.KindUnexpected, err, "build multipart header")
		}
		counter.size += part.size
	}
	if err := writer.Close(); err != nil {
		return 0, apperr.Wrap(apperr.KindUnexpected, err, "finish multipart body")
	}
	return counter.size, nil
}

func writeAPIMultipart(destination io.Writer, boundary string, parts []apiFormPart) error {
	writer := multipart.NewWriter(destination)
	if err := writer.SetBoundary(boundary); err != nil {
		return err
	}
	for _, part := range parts {
		output, err := writer.CreatePart(apiMultipartHeader(part))
		if err != nil {
			return err
		}
		if _, err := io.Copy(output, part.reader); err != nil {
			return err
		}
	}
	return writer.Close()
}

func apiMultipartHeader(part apiFormPart) textproto.MIMEHeader {
	parameters := map[string]string{"name": part.name}
	if part.filename != "" {
		parameters["filename"] = part.filename
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", parameters))
	if part.contentType != "" {
		header.Set("Content-Type", part.contentType)
	}
	return header
}

type apiCountingWriter struct{ size int64 }

func (w *apiCountingWriter) Write(value []byte) (int, error) {
	w.size += int64(len(value))
	return len(value), nil
}

type apiMultipartReader struct {
	reader   *io.PipeReader
	writer   *io.PipeWriter
	boundary string
	parts    []apiFormPart
	once     sync.Once
}

func newAPIMultipartReader(boundary string, parts []apiFormPart) *apiMultipartReader {
	reader, writer := io.Pipe()
	return &apiMultipartReader{reader: reader, writer: writer, boundary: boundary, parts: parts}
}

func (r *apiMultipartReader) Read(value []byte) (int, error) {
	r.once.Do(func() {
		go func() {
			err := writeAPIMultipart(r.writer, r.boundary, r.parts)
			_ = r.writer.CloseWithError(err)
		}()
	})
	return r.reader.Read(value)
}

func (r *apiMultipartReader) Close() error {
	_ = r.writer.Close()
	return r.reader.Close()
}

func usesAPIStdin(options apiOptions) int {
	uses := 0
	if options.input == "-" {
		uses++
	}
	for _, field := range options.fields {
		if field.kind != apiTypedField {
			continue
		}
		_, raw, found := strings.Cut(field.raw, "=")
		if found && raw == "@-" {
			uses++
		}
	}
	for _, form := range options.forms {
		_, raw, found := strings.Cut(form, "=")
		if found && raw == "@-" {
			uses++
		}
	}
	return uses
}

func parseAPIHeaders(values []string, hasBody, form bool) (http.Header, error) {
	headers := make(http.Header)
	headers.Set("Accept", "application/json")
	if hasBody && !form {
		headers.Set("Content-Type", "application/json")
	}
	for _, value := range values {
		name, headerValue, found := strings.Cut(value, ":")
		name = strings.TrimSpace(name)
		if !found {
			return nil, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("header %q must be 'Name: value'", value))
		}
		lower := strings.ToLower(name)
		if _, protected := apiProtectedHeaders[lower]; protected {
			return nil, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("header %q is managed by jiro", name))
		}
		if form && lower == "content-type" {
			return nil, apperr.New(apperr.KindInvalidInput, "Content-Type cannot be customized in form mode")
		}
		headerValue = strings.TrimSpace(headerValue)
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if headerValue == "" {
			headers.Del(canonical)
		} else {
			headers.Add(canonical, headerValue)
		}
	}
	return headers, nil
}

func (a *app) renderAPIResponse(response *http.Response, include bool) error {
	success := response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
	writer := a.stderr
	if success {
		writer = a.stdout
	}
	if include {
		if err := writeAPIResponseHeaders(writer, response); err != nil {
			if success && isBrokenPipe(err) {
				return nil
			}
			return err
		}
	}
	destination := writer
	if success && a.quiet {
		destination = io.Discard
	}
	written, readErr, writeErr := copyAPIStream(destination, response.Body)
	if writeErr != nil {
		if success && isBrokenPipe(writeErr) {
			return nil
		}
		return writeErr
	}
	if readErr != nil {
		return apperr.Wrap(apperr.KindAPI, readErr, "read Jira API response: %v", readErr)
	}
	if success {
		return nil
	}
	if written == 0 {
		if _, err := fmt.Fprintf(a.stderr, "Jira API returned HTTP %s\n", response.Status); err != nil {
			return err
		}
	}
	kind := jira.ClassifyStatus(response.StatusCode)
	err := &apperr.Error{Kind: kind, Message: "Jira API returned HTTP " + response.Status, StatusCode: response.StatusCode}
	return &apiRenderedError{error: err}
}

func writeAPIResponseHeaders(writer io.Writer, response *http.Response) error {
	if _, err := fmt.Fprintf(writer, "%s %s\r\n", response.Proto, response.Status); err != nil {
		return err
	}
	names := make([]string, 0, len(response.Header))
	for name := range response.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		values := response.Header.Values(name)
		for _, value := range values {
			if _, err := fmt.Fprintf(writer, "%s: %s\r\n", name, value); err != nil {
				return err
			}
		}
	}
	_, err := io.WriteString(writer, "\r\n")
	return err
}

func copyAPIStream(destination io.Writer, source io.Reader) (written int64, readErr, writeErr error) {
	buffer := make([]byte, 32*1024)
	for {
		count, err := source.Read(buffer)
		if count > 0 {
			output, outputErr := destination.Write(buffer[:count])
			written += int64(output)
			if outputErr != nil {
				return written, nil, outputErr
			}
			if output != count {
				return written, nil, io.ErrShortWrite
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return written, nil, nil
			}
			return written, err, nil
		}
	}
}
