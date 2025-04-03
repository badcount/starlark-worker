package cadence

import (
	"github.com/cadence-workflow/starlark-worker/internal/backend"
	"github.com/cadence-workflow/starlark-worker/internal/encoded"
	"github.com/cadence-workflow/starlark-worker/internal/worker"
	"github.com/cadence-workflow/starlark-worker/internal/workflow"
	"github.com/uber-go/tally"
	"go.uber.org/cadence"
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	cadactivity "go.uber.org/cadence/activity"
	cadworker "go.uber.org/cadence/worker"
	cad "go.uber.org/cadence/workflow"
	"go.uber.org/yarpc"
	"go.uber.org/yarpc/api/transport"
	"go.uber.org/yarpc/transport/grpc"
	"go.uber.org/yarpc/transport/tchannel"
	"go.uber.org/zap"
	"log"
	"net/url"
	"reflect"
	"time"
)

func GetBackend() backend.Backend {
	return cadenceBackend{}
}

type cadenceBackend struct {
}

var _ backend.Backend = (*cadenceBackend)(nil)

func (c cadenceBackend) RegisterWorker(url string, domain string, taskList string, logger *zap.Logger) worker.Worker {
	cadInterface := NewInterface(url)
	worker := cadworker.New(
		cadInterface,
		domain,
		taskList,
		cadworker.Options{
			MetricsScope: tally.NoopScope,
			Logger:       logger,
			DataConverter: &DataConverter{
				Logger: logger,
			},
			ContextPropagators: []cad.ContextPropagator{
				&HeadersContextPropagator{},
			},
		},
	)
	return &cadenceWorker{w: worker}
}

type cadenceWorkflow struct{}

type workflowInfo struct {
	context cad.Context
}

type cadenceFuture struct {
	f cad.Future
}

type cadenceChildWorkflowFuture struct {
	cf cad.ChildWorkflowFuture
}

type cadenceSettable struct {
	s cad.Settable
}

type cadenceWorker struct {
	w cadworker.Worker
}

func (w *cadenceWorker) RegisterWorkflow(wf interface{}) {
	originalFunc := reflect.ValueOf(wf)
	originalType := originalFunc.Type()

	if originalType.Kind() != reflect.Func || originalType.NumIn() == 0 {
		panic("workflow function must be a function and have at least one argument (context)")
	}

	// Build a new function with the same signature but context converted to cadence.Context
	wrappedFuncType := reflect.FuncOf(
		append([]reflect.Type{reflect.TypeOf((*cad.Context)(nil)).Elem()}, worker.GetRemainingInTypes(originalType)...),
		worker.GetOutTypes(originalType),
		false,
	)

	wrappedFunc := reflect.MakeFunc(wrappedFuncType, func(args []reflect.Value) []reflect.Value {
		// Replace cadence.Context with workflow.Context for original call
		newArgs := make([]reflect.Value, len(args))
		newArgs[0] = args[0].Convert(reflect.TypeOf((*cad.Context)(nil)).Elem()) // keep as cadence.Context
		for i := 1; i < len(args); i++ {
			newArgs[i] = args[i]
		}
		return originalFunc.Call(newArgs)
	})

	w.w.RegisterWorkflow(wrappedFunc.Interface())
}

func (w *cadenceWorker) RegisterActivity(a interface{}) {
	w.w.RegisterActivity(a)
}

func (w *cadenceWorker) Start() error {
	return w.w.Start()
}

func (w *cadenceWorker) Run(_ <-chan interface{}) error {
	return w.w.Run()
}

func (w *cadenceWorker) Stop() {
	w.w.Stop()
}

var _ worker.Worker = (*cadenceWorker)(nil)

func (w *cadenceWorker) RegisterWorkflowWithOptions(runFunc interface{}, options worker.RegisterWorkflowOptions) {
	w.w.RegisterWorkflowWithOptions(runFunc, cad.RegisterOptions{
		Name:                          options.Name,
		EnableShortName:               options.EnableShortName,
		DisableAlreadyRegisteredCheck: options.DisableAlreadyRegisteredCheck,
	})
}

func (w *cadenceWorker) RegisterActivityWithOptions(runFunc interface{}, options worker.RegisterActivityOptions) {
	w.w.RegisterActivityWithOptions(runFunc, cadactivity.RegisterOptions{
		Name:                          options.Name,
		EnableShortName:               options.EnableShortName,
		DisableAlreadyRegisteredCheck: options.DisableAlreadyRegisteredCheck,
	})
}

func (s *cadenceSettable) SetValue(value interface{}) {
	s.s.SetValue(value)
}

func (s *cadenceSettable) SetError(err error) {
	s.s.SetError(err)
}

func (s *cadenceSettable) Set(value interface{}, err error) {
	s.s.Set(value, err)
}

func (s *cadenceSettable) Chain(future workflow.Future) {
	s.s.Chain(future.(*cadenceFuture).f)
}

func (f *cadenceFuture) Get(ctx workflow.Context, valPtr interface{}) error {
	return f.f.Get(ctx.(cad.Context), valPtr)
}

func (f *cadenceFuture) IsReady() bool {
	return f.f.IsReady()
}

func (f *cadenceChildWorkflowFuture) Get(ctx workflow.Context, valPtr interface{}) error {
	return f.cf.Get(ctx.(cad.Context), valPtr)
}

func (f *cadenceChildWorkflowFuture) IsReady() bool {
	return f.cf.IsReady()
}
func (f *cadenceChildWorkflowFuture) GetChildWorkflowExecution() workflow.Future {
	future := f.cf.GetChildWorkflowExecution()
	return &cadenceFuture{f: future}
}

func (f *cadenceChildWorkflowFuture) SignalChildWorkflow(ctx workflow.Context, signalName string, data interface{}) workflow.Future {
	future := f.cf.SignalChildWorkflow(ctx.(cad.Context), signalName, data)
	return &cadenceFuture{f: future}
}

func (w *workflowInfo) ExecutionID() string {
	return cad.GetInfo(w.context).WorkflowExecution.ID
}
func (w *workflowInfo) RunID() string {
	return cad.GetInfo(w.context).WorkflowExecution.RunID
}

var _ workflow.Workflow = (*cadenceWorkflow)(nil)

func (w cadenceWorkflow) GetLogger(ctx workflow.Context) *zap.Logger {
	return cad.GetLogger(ctx.(cad.Context))
}

func (w cadenceWorkflow) GetInfo(ctx workflow.Context) workflow.IInfo {
	return &workflowInfo{
		context: ctx.(cad.Context),
	}
}

func (w cadenceWorkflow) ExecuteActivity(ctx workflow.Context, activity interface{}, args ...interface{}) workflow.Future {
	f := cad.ExecuteActivity(ctx.(cad.Context), activity, args...)
	return &cadenceFuture{f: f}
}

func (w cadenceWorkflow) WithValue(parent workflow.Context, key interface{}, val interface{}) workflow.Context {
	return cad.WithValue(parent.(cad.Context), key, val)
}

func (w cadenceWorkflow) NewDisconnectedContext(parent workflow.Context) (ctx workflow.Context, cancel func()) {
	return cad.NewDisconnectedContext(parent.(cad.Context))
}

func (w cadenceWorkflow) GetMetricsScope(ctx workflow.Context) tally.Scope {
	return cad.GetMetricsScope(ctx.(cad.Context))
}

func (w *cadenceWorkflow) WithTaskList(ctx workflow.Context, name string) workflow.Context {
	return cad.WithTaskList(ctx.(cad.Context), name)
}

func (w *cadenceWorkflow) WithActivityOptions(ctx workflow.Context, options workflow.ActivityOptions) workflow.Context {
	cadOptions := cad.ActivityOptions{
		TaskList:               options.TaskList,
		ScheduleToCloseTimeout: options.ScheduleToCloseTimeout,
		ScheduleToStartTimeout: options.ScheduleToStartTimeout,
		StartToCloseTimeout:    options.StartToCloseTimeout,
		HeartbeatTimeout:       options.HeartbeatTimeout,
		WaitForCancellation:    options.WaitForCancellation,
		ActivityID:             options.ActivityID,
	}
	if options.RetryPolicy != nil {
		cadOptions.RetryPolicy = &cad.RetryPolicy{
			InitialInterval:          options.RetryPolicy.InitialInterval,
			BackoffCoefficient:       options.RetryPolicy.BackoffCoefficient,
			MaximumInterval:          options.RetryPolicy.MaximumInterval,
			ExpirationInterval:       options.RetryPolicy.ExpirationInterval,
			MaximumAttempts:          options.RetryPolicy.MaximumAttempts,
			NonRetriableErrorReasons: options.RetryPolicy.NonRetriableErrorReasons,
		}
	}
	return cad.WithActivityOptions(ctx.(cad.Context), cadOptions)
}

func (w cadenceWorkflow) WithChildOptions(ctx workflow.Context, cwo workflow.ChildWorkflowOptions) workflow.Context {
	cadOptions := cad.ChildWorkflowOptions{
		Domain:                       cwo.Domain,
		WorkflowID:                   cwo.WorkflowID,
		TaskList:                     cwo.TaskList,
		ExecutionStartToCloseTimeout: cwo.ExecutionStartToCloseTimeout,
		TaskStartToCloseTimeout:      cwo.TaskStartToCloseTimeout,
		WaitForCancellation:          cwo.WaitForCancellation,
		CronSchedule:                 cwo.Domain,
		Memo:                         cwo.Memo,
		SearchAttributes:             cwo.SearchAttributes,
	}
	if cwo.RetryPolicy != nil {
		cadOptions.RetryPolicy = &cad.RetryPolicy{
			InitialInterval:          cwo.RetryPolicy.InitialInterval,
			BackoffCoefficient:       cwo.RetryPolicy.BackoffCoefficient,
			MaximumInterval:          cwo.RetryPolicy.MaximumInterval,
			ExpirationInterval:       cwo.RetryPolicy.ExpirationInterval,
			MaximumAttempts:          cwo.RetryPolicy.MaximumAttempts,
			NonRetriableErrorReasons: cwo.RetryPolicy.NonRetriableErrorReasons,
		}
	}
	return cad.WithChildOptions(ctx.(cad.Context), cadOptions)
}

func (w cadenceWorkflow) SetQueryHandler(ctx workflow.Context, queryType string, handler interface{}) error {
	return cad.SetQueryHandler(ctx.(cad.Context), queryType, handler)
}

func (w cadenceWorkflow) WithWorkflowDomain(ctx workflow.Context, name string) workflow.Context {
	return cad.WithWorkflowDomain(ctx.(cad.Context), name)
}

func (w cadenceWorkflow) WithWorkflowTaskList(ctx workflow.Context, name string) workflow.Context {
	return cad.WithWorkflowTaskList(ctx.(cad.Context), name)
}

func (w cadenceWorkflow) ExecuteChildWorkflow(ctx workflow.Context, childWorkflow interface{}, args ...interface{}) workflow.ChildWorkflowFuture {
	f := cad.ExecuteChildWorkflow(ctx.(cad.Context), childWorkflow, args...)
	return &cadenceChildWorkflowFuture{cf: f}
}

func (w cadenceWorkflow) NewCustomError(reason string, details ...interface{}) error {
	return cadence.NewCustomError(reason, details...)
}

func (w cadenceWorkflow) NewFuture(ctx workflow.Context) (workflow.Future, workflow.Settable) {
	f, s := cad.NewFuture(ctx.(cad.Context))
	return &cadenceFuture{f: f}, &cadenceSettable{s: s}
}

func (w cadenceWorkflow) Go(ctx workflow.Context, f func(ctx workflow.Context)) {
	cad.Go(ctx.(cad.Context), func(c cad.Context) {
		f(ctx)
	})
}

func (w cadenceWorkflow) SideEffect(ctx workflow.Context, f func(ctx workflow.Context) interface{}) encoded.Value {
	return cad.SideEffect(ctx.(cad.Context), func(ctx cad.Context) interface{} {
		return f(ctx)
	})
}

func (w cadenceWorkflow) Now(ctx workflow.Context) time.Time {
	return cad.Now(ctx.(cad.Context))
}

func (w cadenceWorkflow) Sleep(ctx workflow.Context, d time.Duration) (err error) {
	return cad.Sleep(ctx.(cad.Context), d)
}

func NewInterface(location string) workflowserviceclient.Interface {
	loc, err := url.Parse(location)
	if err != nil {
		log.Fatalln(err)
	}

	var tran transport.UnaryOutbound
	switch loc.Scheme {
	case "grpc":
		tran = grpc.NewTransport().NewSingleOutbound(loc.Host)
	case "tchannel":
		if t, err := tchannel.NewTransport(tchannel.ServiceName("tchannel")); err != nil {
			log.Fatalln(err)
		} else {
			tran = t.NewSingleOutbound(loc.Host)
		}
	default:
		log.Fatalf("unsupported scheme: %s", loc.Scheme)
	}

	service := "cadence-frontend"
	dispatcher := yarpc.NewDispatcher(yarpc.Config{
		Name: service,
		Outbounds: yarpc.Outbounds{
			service: {
				Unary: tran,
			},
		},
	})
	if err := dispatcher.Start(); err != nil {
		log.Fatalln(err)
	}
	return workflowserviceclient.New(dispatcher.ClientConfig(service))
}
