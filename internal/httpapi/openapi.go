package httpapi

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3gen"
	"github.com/google/uuid"

	"github.com/pavelananev/dechat/internal/models"
)

// specTitle/specVersion centralize the document metadata used both by the
// served spec and by cmd/openapi which dumps it to disk.
const (
	specTitle   = "deChat API"
	specVersion = "1.0.0"
)

// BuildSpec constructs the OpenAPI 3.0 document for the deChat REST API.
//
// This is a parallel declaration of the routes in router.go: every route
// mounted there has a matching operation here, and the JSON schemas are
// derived from the actual Go request/response structs via openapi3gen, so
// they can never silently drift from what the handlers marshal. When you add
// a route, add its operation here too.
//
// The WebSocket endpoint (GET /api/v1/ws) is intentionally NOT modeled as a
// path: OpenAPI 3.0 has no concept of WebSocket. Its ClientMsg / ServerMsg
// envelope types are still registered as named components so WS clients have
// a schema to codegen against.
func BuildSpec() (*openapi3.T, error) {
	schemas := openapi3.Schemas{}

	// Register every reusable schema up front so operations can reference them
	// by name. Anything referenced from a request body or response must land
	// here first.
	//
	// We use NewSchemaRefForValue (not GenerateSchemaRef) because the latter
	// emits a $ref for *every* Go type — including primitives like string —
	// expecting the caller to also register "string", "int" etc. as named
	// components. NewSchemaRefForValue instead inlines primitive types and
	// only $refs cyclical/user types, which is what we want for a clean,
	// self-contained components block.
	type schemaEntry struct {
		name string
		v    any
	}
	for _, e := range []schemaEntry{
		{"User", models.User{}},
		{"Room", models.Room{}},
		{"Message", models.Message{}},
		{"LoginRequest", loginRequest{}},
		{"LoginResponse", loginResponse{}},
		{"CreateUserRequest", createUserRequest{}},
		{"CreateRoomRequest", CreateRoomRequest{}},
		// WebSocket envelopes (documented in info.description, not as a path).
		{"ClientMsg", models.ClientMsg{}},
		{"ServerMsg", models.ServerMsg{}},
	} {
		ref, err := openapi3gen.NewSchemaRefForValue(e.v, schemas)
		if err != nil {
			return nil, fmt.Errorf("generate schema %q: %w", e.name, err)
		}
		// NewSchemaRefForValue returns the inline schema; promote it to a named
		// component so operations can $ref it and the doc stays DRY.
		schemas[e.name] = &openapi3.SchemaRef{Value: ref.Value}

		// openapi3gen does not understand github.com/google/uuid.UUID (a
		// [16]byte that marshals as a string), so it leaves those fields with
		// an empty schema. Patch them to the conventional {type:string,
		// format:uuid} by walking the struct tags directly.
		patchUUIDFields(e.v, schemas[e.name].Value)
	}

	// Shared building blocks --------------------------------------------------

	// Single error envelope used by every 4xx/5xx response.
	errSchema := openapi3.NewSchema().WithProperties(map[string]*openapi3.Schema{
		"error": openapi3.NewStringSchema(),
	})
	errResp := func() *openapi3.ResponseRef {
		return &openapi3.ResponseRef{Value: openapi3.NewResponse().
			WithDescription("Error").
			WithJSONSchema(errSchema)}
	}

	empty204 := func() *openapi3.ResponseRef {
		// 204 has no body; an empty description is the conventional shape.
		return &openapi3.ResponseRef{Value: openapi3.NewResponse().WithDescription("No content")}
	}

	// refResp returns a 200/201 response whose body is the named component.
	refResp := func(desc, schemaName string) *openapi3.ResponseRef {
		return &openapi3.ResponseRef{Value: openapi3.NewResponse().
			WithDescription(desc).
			WithJSONSchemaRef(openapi3.NewSchemaRef("#/components/schemas/"+schemaName, nil))}
	}

	// refBody returns a request body pointing at a named component.
	refBody := func(schemaName string) *openapi3.RequestBodyRef {
		return &openapi3.RequestBodyRef{Value: openapi3.NewRequestBody().
			WithRequired(true).
			WithJSONSchemaRef(openapi3.NewSchemaRef("#/components/schemas/"+schemaName, nil))}
	}

	// listResp returns a 200 response whose body is an array of a named
	// component, e.g. []#User.
	listResp := func(desc, itemSchemaName string) *openapi3.ResponseRef {
		arr := openapi3.NewArraySchema()
		arr.Items = openapi3.NewSchemaRef("#/components/schemas/"+itemSchemaName, nil)
		return &openapi3.ResponseRef{Value: openapi3.NewResponse().
			WithDescription(desc).
			WithJSONSchema(arr)}
	}

	uuidPath := func(name, desc string) *openapi3.ParameterRef {
		p := openapi3.NewPathParameter(name).
			WithSchema(openapi3.NewUUIDSchema()).
			WithDescription(desc)
		return &openapi3.ParameterRef{Value: p}
	}

	// jwt marks an operation as requiring the bearer token. Each secured op
	// gets its own Security pointer so they don't alias.
	jwt := func() *openapi3.SecurityRequirements {
		s := openapi3.SecurityRequirements{{"bearerAuth": []string{}}}
		return &s
	}

	// responses builds a *openapi3.Responses from (code, ref) pairs. Every
	// operation MUST declare at least one response per the spec.
	responses := func(pairs ...struct {
		code string
		r    *openapi3.ResponseRef
	}) *openapi3.Responses {
		rs := openapi3.NewResponses()
		for _, p := range pairs {
			rs.Set(p.code, p.r)
		}
		return rs
	}

	rr := func(code string, r *openapi3.ResponseRef) struct {
		code string
		r    *openapi3.ResponseRef
	} {
		return struct {
			code string
			r    *openapi3.ResponseRef
		}{code, r}
	}

	paths := openapi3.NewPaths()

	// ---- Public: POST /login -----------------------------------------------

	paths.Set("/api/v1/login", &openapi3.PathItem{
		Post: &openapi3.Operation{
			Tags:        []string{"auth"},
			Summary:     "Log in",
			Description: "Authenticate with username + password; returns a JWT. Blocked users cannot log in.",
			RequestBody: refBody("LoginRequest"),
			Responses: responses(
				rr("200", refResp("OK", "LoginResponse")),
				rr("400", errResp()),
				rr("401", errResp()),
			),
		},
	})

	// ---- Authed: GET /me ----------------------------------------------------

	paths.Set("/api/v1/me", &openapi3.PathItem{
		Get: &openapi3.Operation{
			Tags:        []string{"auth"},
			Summary:     "Current user",
			Description: "Returns the caller's profile derived from the JWT claims.",
			Security:    jwt(),
			Responses: responses(
				rr("200", &openapi3.ResponseRef{Value: openapi3.NewResponse().
					WithDescription("OK").
					WithJSONSchema(openapi3.NewObjectSchema().WithProperties(map[string]*openapi3.Schema{
						"user_id":  openapi3.NewUUIDSchema(),
						"username": openapi3.NewStringSchema(),
						"role":     openapi3.NewStringSchema(),
					}))}),
				rr("401", errResp()),
			),
		},
	})

	// ---- Authed: rooms (list/create) ---------------------------------------

	paths.Set("/api/v1/rooms", &openapi3.PathItem{
		Get: &openapi3.Operation{
			Tags:        []string{"rooms"},
			Summary:     "List rooms",
			Description: "Returns every room. In v1 all authenticated users see all rooms.",
			Security:    jwt(),
			Responses: responses(
				rr("200", listResp("OK", "Room")),
				rr("401", errResp()),
			),
		},
		Post: &openapi3.Operation{
			Tags:        []string{"rooms", "admin"},
			Summary:     "Create room",
			Description: "Admin only.",
			Security:    jwt(),
			RequestBody: refBody("CreateRoomRequest"),
			Responses: responses(
				rr("201", refResp("Created", "Room")),
				rr("400", errResp()),
				rr("401", errResp()),
				rr("403", errResp()),
				rr("409", errResp()),
			),
		},
	})

	// ---- Authed: room messages ---------------------------------------------

	limitParam := &openapi3.ParameterRef{Value: openapi3.NewQueryParameter("limit").
		WithSchema(openapi3.NewIntegerSchema().WithMin(1).WithMax(100).WithDefault(50)).
		WithDescription("Max items per page (1..100, default 50).")}
	beforeParam := &openapi3.ParameterRef{Value: openapi3.NewQueryParameter("before").
		WithSchema(openapi3.NewDateTimeSchema()).
		WithDescription("RFC 3339 timestamp cursor; return messages older than this.")}

	paths.Set("/api/v1/rooms/{id}/messages", &openapi3.PathItem{
		Get: &openapi3.Operation{
			Tags:        []string{"rooms", "messages"},
			Summary:     "List room messages",
			Description: "Returns a page of a room's message history, newest first. Because results are newest-first, fetch the next older page by passing the oldest message's created_at as `before`.",
			Security:    jwt(),
			Parameters: openapi3.Parameters{
				uuidPath("id", "Room ID"),
				limitParam,
				beforeParam,
			},
			Responses: responses(
				rr("200", listResp("OK", "Message")),
				rr("400", errResp()),
				rr("401", errResp()),
				rr("404", errResp()),
			),
		},
	})

	// ---- Admin: room management -------------------------------------------

	paths.Set("/api/v1/rooms/{id}", &openapi3.PathItem{
		Delete: &openapi3.Operation{
			OperationID: "deleteRoom",
			Tags:        []string{"rooms", "admin"},
			Summary:     "Delete room",
			Description: "Admin only. Deletes the room and all of its messages (cascade).",
			Security:    jwt(),
			Parameters:  openapi3.Parameters{uuidPath("id", "Room ID")},
			Responses: responses(
				rr("204", empty204()),
				rr("401", errResp()),
				rr("403", errResp()),
				rr("404", errResp()),
			),
		},
	})

	paths.Set("/api/v1/rooms/{id}/clear", &openapi3.PathItem{
		Post: &openapi3.Operation{
			OperationID: "clearRoom",
			Tags:        []string{"rooms", "admin"},
			Summary:     "Clear room",
			Description: "Admin only. Deletes all messages in the room but keeps the room itself.",
			Security:    jwt(),
			Parameters:  openapi3.Parameters{uuidPath("id", "Room ID")},
			Responses: responses(
				rr("204", empty204()),
				rr("401", errResp()),
				rr("403", errResp()),
				rr("404", errResp()),
			),
		},
	})

	// ---- Admin: user management -------------------------------------------

	paths.Set("/api/v1/admin/users", &openapi3.PathItem{
		Get: &openapi3.Operation{
			OperationID: "listUsers",
			Tags:        []string{"users", "admin"},
			Summary:     "List users",
			Description: "Admin only. Returns every user account.",
			Security:    jwt(),
			Responses: responses(
				rr("200", listResp("OK", "User")),
				rr("401", errResp()),
				rr("403", errResp()),
			),
		},
		Post: &openapi3.Operation{
			OperationID: "createUser",
			Tags:        []string{"users", "admin"},
			Summary:     "Create user",
			Description: "Admin only. Creates a new user account. There is no self-registration.",
			Security:    jwt(),
			RequestBody: refBody("CreateUserRequest"),
			Responses: responses(
				rr("201", refResp("Created", "User")),
				rr("400", errResp()),
				rr("401", errResp()),
				rr("403", errResp()),
				rr("409", errResp()),
			),
		},
	})

	// /admin/users/{id} only supports DELETE. Block and unblock are modeled as
	// separate sub-paths (matching the router) so each gets a distinct,
	// codegen-friendly operationId.
	paths.Set("/api/v1/admin/users/{id}", &openapi3.PathItem{
		Delete: &openapi3.Operation{
			OperationID: "deleteUser",
			Tags:        []string{"users", "admin"},
			Summary:     "Delete user",
			Description: "Admin only. Deletes the user; their messages are removed via ON DELETE CASCADE.",
			Security:    jwt(),
			Parameters:  openapi3.Parameters{uuidPath("id", "User ID")},
			Responses: responses(
				rr("204", empty204()),
				rr("401", errResp()),
				rr("403", errResp()),
				rr("404", errResp()),
			),
		},
	})

	paths.Set("/api/v1/admin/users/{id}/block", &openapi3.PathItem{
		Post: &openapi3.Operation{
			OperationID: "blockUser",
			Tags:        []string{"users", "admin"},
			Summary:     "Block user",
			Description: "Admin only. Disables the user immediately; revokes access and rejects future logins. Existing JWTs stop working on the next request and the WS handshake is rejected; active WS connections drop on reconnect.",
			Security:    jwt(),
			Parameters:  openapi3.Parameters{uuidPath("id", "User ID")},
			Responses: responses(
				rr("204", empty204()),
				rr("401", errResp()),
				rr("403", errResp()),
				rr("404", errResp()),
			),
		},
	})

	paths.Set("/api/v1/admin/users/{id}/unblock", &openapi3.PathItem{
		Post: &openapi3.Operation{
			OperationID: "unblockUser",
			Tags:        []string{"users", "admin"},
			Summary:     "Unblock user",
			Description: "Admin only. Re-enables a previously blocked user; they can log in again.",
			Security:    jwt(),
			Parameters:  openapi3.Parameters{uuidPath("id", "User ID")},
			Responses: responses(
				rr("204", empty204()),
				rr("401", errResp()),
				rr("403", errResp()),
				rr("404", errResp()),
			),
		},
	})

	doc := &openapi3.T{
		OpenAPI: "3.0.3",
		Info: &openapi3.Info{
			Title:       specTitle,
			Version:     specVersion,
			Description: description,
			License:     &openapi3.License{Name: "MIT"},
		},
		Paths: paths,
		Components: &openapi3.Components{
			Schemas:         schemas,
			SecuritySchemes: openapi3.SecuritySchemes{"bearerAuth": bearerScheme()},
		},
		Security: openapi3.SecurityRequirements{{"bearerAuth": []string{}}},
	}

	// kin-openapi's doc.Validate is intentionally not called: its SchemaRef
	// validator treats any $ref whose in-memory Value is nil (i.e. any ref
	// resolved via components rather than inlined) as "unresolved", which
	// would reject a perfectly valid document. Real consumers (Swagger UI,
	// editor.swagger.io, codegen tools) resolve $ref against
	// components.schemas correctly. Instead we run a focused self-check:
	// every $ref this builder emits must point at a registered component.
	if err := checkRefs(doc); err != nil {
		return nil, fmt.Errorf("validate spec: %w", err)
	}
	return doc, nil
}

// checkRefs walks the marshalled JSON and asserts every "$ref" points at a
// schema/securityScheme defined under components. This catches the typos
// doc.Validate would (refs to nonexistent components) without tripping on
// kin's over-strict inline-ref rule.
func checkRefs(doc *openapi3.T) error {
	known := map[string]struct{}{}
	for name := range doc.Components.Schemas {
		known["#/components/schemas/"+name] = struct{}{}
	}
	for name := range doc.Components.SecuritySchemes {
		known["#/components/securitySchemes/"+name] = struct{}{}
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	var visit func(v any, path string) error
	visit = func(v any, path string) error {
		switch t := v.(type) {
		case map[string]any:
			if ref, ok := t["$ref"].(string); ok {
				if _, ok := known[ref]; !ok {
					return fmt.Errorf("unresolved $ref %q at %s", ref, path)
				}
			}
			for k, child := range t {
				if err := visit(child, path+"/"+k); err != nil {
					return err
				}
			}
		case []any:
			for i, child := range t {
				if err := visit(child, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
		return nil
	}

	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	return visit(root, "")
}

// bearerScheme describes the JWT auth used across the API.
func bearerScheme() *openapi3.SecuritySchemeRef {
	return &openapi3.SecuritySchemeRef{
		Value: openapi3.NewSecurityScheme().
			WithType("http").
			WithScheme("bearer").
			WithBearerFormat("JWT").
			WithDescription("Paste the JWT returned by POST /api/v1/login."),
	}
}

// patchUUIDFields walks example's struct fields and, for any field whose Go
// type is uuid.UUID (or *uuid.UUID), overwrites the corresponding property in
// schema with {type: string, format: uuid}. This compensates for openapi3gen
// not knowing about uuid.UUID and leaving it as an empty schema.
//
// The JSON property name is derived from the field's `json` tag (falling back
// to the field name), matching encoding/json's own resolution so the patched
// property always lines up with what openapi3gen emitted.
func patchUUIDFields(example any, schema *openapi3.Schema) {
	if schema == nil || schema.Properties == nil {
		return
	}
	t := reflect.TypeOf(example)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft != reflect.TypeOf(uuid.UUID{}) {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			name = f.Name
		}
		if _, ok := schema.Properties[name]; ok {
			schema.Properties[name] = &openapi3.SchemaRef{
				Value: openapi3.NewStringSchema().WithFormat("uuid"),
			}
		}
	}
}

// description documents the WebSocket endpoint in prose, since OpenAPI 3.0 has
// no native model for WebSocket. It also points readers at the envelope
// schemas registered under components.
const description = `A real-time chat backend for a private group.

**Auth:** all endpoints except ` + "`POST /login`" + ` require an
` + "`Authorization: Bearer <jwt>`" + ` header.

**Real-time messaging** happens over a WebSocket at ` + "`GET /api/v1/ws`" + `,
which authenticates with the JWT passed as a ` + "`?token=`" + ` query parameter
(browsers cannot set custom headers on the WS handshake). After connecting the
client sends ` + "`ClientMsg`" + ` envelopes and receives ` + "`ServerMsg`" + `
envelopes; both shapes are documented under components.schemas.

There is **no open registration** — every account is created by an
administrator. Messages are stored indefinitely; an admin can clear a room on
demand. In v1 every authenticated user can see every room.`
