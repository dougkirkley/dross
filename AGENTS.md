# Holocron Agent Integration Guide

This document provides essential information for agents working with the Holocron application, which combines a Vue.js frontend with a Go backend to manage multiple AI provider integrations.

## Overview

Holocron is a web application that allows users to interact with different AI providers (Claude, Gemini, Antigravity, CodeX) through a unified UI. It uses server-sent events (SSE) for real-time communication between the frontend and backend.

## Architecture

### Backend
- **Go-based server** with `net/http` for routing
- **AI Manager** (`internal/ai/manager.go`) that handles provider registration and command routing
- **Provider implementations** in `internal/ai/` directory:
  - Claude
  - Gemini  
  - Antigravity
  - CodeX
- **SSE Broker** (`internal/server/sse.go`) for real-time communication

### Frontend
- **Vue.js 3** with TypeScript and Vite
- **Pinia** for state management
- **Tailwind CSS** for styling (dark theme)
- **Server-Sent Events** for real-time updates from backend

## Essential Commands

### Development
```bash
# Start development server
npm run dev

# Build for production  
npm run build

# Run tests
npm run test  # Not currently implemented, but expected to follow Vue.js conventions

# Lint and format code
npm run lint
npm run format
```

### Backend
```bash
# Run backend server (assumes Go environment)
go run cmd/holocron/main.go

# Build Go binary
go build -o holocron cmd/holocron/main.go
```

## How Components Fit Together

1. **Frontend** sends command requests to `/api/command` via POST
2. **Backend** receives the request and routes it to the appropriate AI provider through `aiManager.SendCommand()`
3. **AI Provider** processes the prompt and returns a response
4. **Backend** broadcasts the result via Server-Sent Events
5. **Frontend** receives events and updates the UI in real-time

## Key Files for Agent Work

### Backend
- `internal/ai/manager.go` - Core provider manager logic
- `internal/server/handler.go` - HTTP handlers including command processing
- `internal/server/sse.go` - Server-Sent Events implementation
- `cmd/holocron/main.go` - Main application entry point

### Frontend  
- `frontend/src/App.vue` - Main UI component with chat interface
- `frontend/src/main.ts` - Application bootstrap
- `frontend/package.json` - Project dependencies and scripts

## Naming Conventions

### Go
- UpperCamelCase for types and functions (`SendMessage`, `NewManager`)
- Lowercase for package names (`ai`, `server`)
- Struct fields start with uppercase letters for public access

### TypeScript/Vue
- PascalCase for component names (`App.vue`)
- camelCase for variables and functions  
- File names reflect component naming (`main.ts`, `App.vue`)

## Testing Approach

### Frontend (Vue.js)
- Uses Vue Test Utils or Vitest (indicated by package.json)
- Component testing focused on UI interactions and state management
- End-to-end tests with Playwright or Cypress recommended for full workflow verification

### Backend (Go)
- Unit tests via `go test` command
- Integration tests covering API endpoints and AI provider integrations
- Mock providers for offline development/testing

## Important Gotchas

1. **SSE Implementation**: The frontend expects `EventSource` to connect to `http://localhost:8080/api/events`. Make sure the backend is running on port 8080.

2. **Context Handling**: In `handleCommand`, a background goroutine uses `context.Background()` because the original request context might be cancelled when the handler returns - this ensures commands complete processing.

3. **Provider Registration**: AI providers are registered dynamically in the manager. The system is extensible and supports adding new providers easily.

4. **Error Handling**: API responses return appropriate HTTP status codes (200, 400, 500) and error messages are broadcast to frontend via SSE.

5. **Cross-Origin Issues**: While running locally with `npm run dev`, browsers might block requests to the Go backend due to CORS limitations. Ensure both are running on localhost if testing from browser.

6. **File Uploads**: The system includes a file upload endpoint (`/api/upload`) that accepts multipart form data and stores files in `/uploads` directory - useful when integrating with AI models that process documents.

## AI Provider Interface

All providers must implement the `Provider` interface:

```go
type Provider interface {
    Name() string               // Returns provider name (e.g. "claude")
    Init(config Config) error   // Initialize provider with config  
    SendMessage(ctx context.Context, prompt string) (string, error)  // Process a command
}
```

Each provider has its own implementation file in `internal/ai/` directory.

## Integration Points for Charm Crush

When integrating Charm Crush components into Holocron:

1. **UI Components** - Create Vue components that can use Crush's AI capabilities directly or through the existing SSE infrastructure
2. **AI Commands** - Add new providers to the manager in `internal/ai/manager.go`
3. **Real-time Updates** - Leverage existing SSE system for real-time responses 
4. **State Management** - Use Pinia stores to manage UI state and agent-specific data

The architecture is intentionally designed to be modular, so adding Crush integration simply involves:
- Extending the provider registry
- Adding appropriate Vue components for UI elements  
- Integrating with the existing event-based communication system if needed