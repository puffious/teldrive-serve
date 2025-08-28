# VadaPav Serve Refactoring Plan

## Current State Analysis

### Architecture Issues
- **Monolithic Design**: Single 824-line `main.go` file containing entire application
- **Global State**: 12+ package-level variables creating tight coupling and testing difficulties
- **Mixed Responsibilities**: HTTP handling, business logic, configuration, and templating all intermixed
- **No Separation of Concerns**: Database logic, API client, HTTP handlers all in one place

### Code Quality Issues
1. **Testing Challenges**: Current structure makes unit testing nearly impossible
2. **Error Handling**: Inconsistent error handling patterns throughout
3. **Configuration**: Environment variables scattered across multiple functions
4. **Maintainability**: Adding new features requires modifying the monolithic main.go
5. **Reusability**: Business logic tightly coupled to HTTP layer

### Performance Concerns
- Global HTTP client shared across all operations
- No proper connection lifecycle management
- Hardcoded buffer sizes and timeouts
- Limited observability and metrics

## Proposed Refactored Architecture

### Directory Structure
```
vadapav-serve/
├── main.go                     # Minimal entry point
├── cmd/
│   └── server/
│       └── main.go            # Server initialization
├── internal/
│   ├── app/                   # Application layer
│   │   ├── app.go            # Application struct and DI container
│   │   └── server.go         # HTTP server setup
│   ├── config/               # Configuration management
│   │   ├── config.go         # Configuration struct and validation
│   │   └── env.go            # Environment variable parsing
│   ├── handlers/             # HTTP request handlers
│   │   ├── browse.go         # Directory browsing handler
│   │   ├── download.go       # File download handler
│   │   ├── upload.go         # Upload handler (optional)
│   │   └── middleware.go     # HTTP middleware
│   ├── services/             # Business logic layer
│   │   ├── file.go           # File operations service
│   │   ├── download.go       # Download management service
│   │   ├── template.go       # Template rendering service
│   │   └── metrics.go        # Metrics and monitoring
│   ├── client/               # External API clients
│   │   ├── teldrive.go       # Teldrive API client
│   │   └── http.go           # HTTP client configuration
│   ├── models/               # Data structures
│   │   ├── file.go           # File-related structs
│   │   └── template.go       # Template data structs
│   └── pkg/                  # Internal packages
│       ├── errors/           # Custom error types
│       │   └── errors.go
│       ├── logger/           # Structured logging
│       │   └── logger.go
│       └── utils/            # Utility functions
│           └── validation.go
├── pkg/                      # Public packages (if any)
├── templates/               # HTML templates (unchanged)
│   ├── index.html
│   └── upload.html
├── tests/                   # Test files
│   ├── integration/
│   └── mocks/
└── docs/                    # Documentation
    └── api.md
```

## Implementation Phases

### Phase 1: Foundation (Week 1)
**Objective**: Establish basic structure and configuration management

#### Tasks:
1. **Create package structure**
   - Set up directory hierarchy
   - Create basic package files with interfaces

2. **Configuration Layer**
   ```go
   type Config struct {
       Server   ServerConfig
       Teldrive TeldriveConfig
       Features FeatureConfig
   }
   ```

3. **Error Handling System**
   ```go
   type AppError struct {
       Code    string
       Message string
       Cause   error
   }
   ```

4. **Logging Infrastructure**
   - Structured logging with levels
   - Request tracing and correlation IDs

### Phase 2: Service Layer (Week 2)
**Objective**: Extract and modularize business logic

#### Tasks:
1. **File Service**
   ```go
   type FileService interface {
       GetFileMetadata(ctx context.Context, fileID string) (*models.File, error)
       ListDirectory(ctx context.Context, path string) ([]models.File, error)
   }
   ```

2. **Download Service**
   ```go
   type DownloadService interface {
       StreamFile(ctx context.Context, fileID string, w http.ResponseWriter, r *http.Request) error
       GetDownloadURL(fileID, filename string) string
   }
   ```

3. **Template Service**
   ```go
   type TemplateService interface {
       RenderDirectory(w http.ResponseWriter, data *models.TemplateData) error
       RenderUploadPage(w http.ResponseWriter) error
   }
   ```

### Phase 3: HTTP Layer (Week 3)
**Objective**: Refactor HTTP handlers and middleware

#### Tasks:
1. **Handler Refactoring**
   - Separate handlers by responsibility
   - Implement dependency injection
   - Add proper error responses

2. **Middleware Implementation**
   - Request logging and metrics
   - Rate limiting
   - Error recovery

3. **Router Setup**
   - Clean route definitions
   - Middleware chaining
   - Graceful shutdown handling

### Phase 4: Client Layer (Week 4)
**Objective**: Abstract external dependencies

#### Tasks:
1. **Teldrive Client**
   ```go
   type TeldriveClient interface {
       GetFile(ctx context.Context, fileID string) (*models.File, error)
       ListFiles(ctx context.Context, path string) ([]models.File, error)
       StreamFile(ctx context.Context, fileID, filename string) (io.ReadCloser, error)
   }
   ```

2. **HTTP Client Configuration**
   - Connection pooling
   - Timeout management
   - Retry logic

3. **Client Mocking**
   - Interface-based mocking
   - Test helpers

### Phase 5: Testing & Documentation (Week 5)
**Objective**: Comprehensive testing and documentation

#### Tasks:
1. **Unit Tests**
   - Service layer tests (90%+ coverage)
   - Handler tests with mocked dependencies
   - Configuration validation tests

2. **Integration Tests**
   - End-to-end request flow
   - Error scenario testing
   - Performance benchmarks

3. **Documentation**
   - API documentation
   - Deployment guides
   - Contributing guidelines

## Implementation Details

### Dependency Injection
```go
type App struct {
    Config          *config.Config
    Logger          *logger.Logger
    FileService     services.FileService
    DownloadService services.DownloadService
    TemplateService services.TemplateService
    TeldriveClient  client.TeldriveClient
}

func NewApp(cfg *config.Config) *App {
    logger := logger.New(cfg.Logger)
    teldriveClient := client.NewTeldriveClient(cfg.Teldrive, logger)
    
    return &App{
        Config:          cfg,
        Logger:          logger,
        FileService:     services.NewFileService(teldriveClient, logger),
        DownloadService: services.NewDownloadService(teldriveClient, logger),
        TemplateService: services.NewTemplateService(cfg.Templates),
        TeldriveClient:  teldriveClient,
    }
}
```

### Configuration Management
```go
type Config struct {
    Server struct {
        Port            int           `env:"PORT" envDefault:"8888"`
        ReadTimeout     time.Duration `env:"READ_TIMEOUT" envDefault:"30s"`
        WriteTimeout    time.Duration `env:"WRITE_TIMEOUT" envDefault:"0"`
        ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"30s"`
    }
    
    Teldrive struct {
        URL   string `env:"TELDRIVE_URL,required"`
        Token string `env:"TELDRIVE_TOKEN,required"`
    }
    
    Features struct {
        EnableUpload         bool `env:"ENABLE_UPLOAD_PAGE" envDefault:"false"`
        MaxConcurrentDL      int  `env:"MAX_CONCURRENT_DOWNLOADS" envDefault:"50"`
        StreamBufferSize     int  `env:"STREAM_BUFFER_SIZE" envDefault:"256"`
        LogConnectionErrors  bool `env:"LOG_CONNECTION_ERRORS" envDefault:"false"`
    }
}
```

### Error Handling Strategy
```go
// Custom error types
type ErrorCode string

const (
    ErrCodeNotFound     ErrorCode = "NOT_FOUND"
    ErrCodeUnauthorized ErrorCode = "UNAUTHORIZED" 
    ErrCodeInternal     ErrorCode = "INTERNAL_ERROR"
    ErrCodeBadRequest   ErrorCode = "BAD_REQUEST"
)

type AppError struct {
    Code    ErrorCode `json:"code"`
    Message string    `json:"message"`
    Details string    `json:"details,omitempty"`
    Cause   error     `json:"-"`
}

func (e *AppError) Error() string {
    return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
```

### Testing Strategy
1. **Unit Tests**: Each service/handler tested in isolation
2. **Integration Tests**: Full request/response cycle testing
3. **Mock Interfaces**: All external dependencies mockable
4. **Benchmark Tests**: Performance regression prevention
5. **Contract Tests**: Teldrive API compatibility testing

## Migration Strategy

### Backward Compatibility
- Maintain existing API endpoints during transition
- Feature flags for new vs old behavior
- Gradual rollout with monitoring

### Deployment Strategy
1. **Blue-Green Deployment**: Zero downtime migration
2. **Feature Toggles**: Enable new architecture incrementally  
3. **Monitoring**: Comprehensive metrics during migration
4. **Rollback Plan**: Quick revert to monolithic version if needed

### Risk Mitigation
1. **Extensive Testing**: Unit, integration, and performance tests
2. **Staged Rollout**: Internal → staging → production
3. **Monitoring**: Real-time metrics and alerting
4. **Documentation**: Clear migration and rollback procedures

## Benefits of Refactored Architecture

### Immediate Benefits
- **Testability**: Unit tests possible for all components
- **Maintainability**: Clear separation of concerns
- **Debugging**: Better error messages and logging
- **Performance**: Optimized connection handling

### Long-term Benefits  
- **Scalability**: Easy to add new features
- **Team Productivity**: Multiple developers can work simultaneously
- **Code Reuse**: Services can be reused across different handlers
- **Documentation**: Self-documenting architecture with interfaces

## Success Metrics

### Code Quality
- Unit test coverage > 90%
- Cyclomatic complexity < 10 per function
- Zero global variables
- All external dependencies mockable

### Performance
- Maintain current download speeds
- Reduce memory usage by 20%
- Improve startup time by 50%
- Better error recovery and retry logic

### Maintainability
- Reduce average PR review time
- Increase developer onboarding speed
- Improve debugging and troubleshooting time