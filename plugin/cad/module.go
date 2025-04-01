package cad

import (
	"fmt"
	"github.com/cadence-workflow/starlark-worker/ext"
	"github.com/cadence-workflow/starlark-worker/internal/workflow"
	"github.com/cadence-workflow/starlark-worker/service"
	"github.com/cadence-workflow/starlark-worker/star"
	"go.starlark.net/starlark"
	"go.uber.org/yarpc/yarpcerrors"
	"go.uber.org/zap"
)

type Module struct {
	info workflow.IInfo
}

var _ starlark.HasAttrs = &Module{}

func (r *Module) String() string                        { return pluginID }
func (r *Module) Type() string                          { return pluginID }
func (r *Module) Freeze()                               {}
func (r *Module) Truth() starlark.Bool                  { return true }
func (r *Module) Hash() (uint32, error)                 { return 0, fmt.Errorf("no-hash") }
func (r *Module) Attr(n string) (starlark.Value, error) { return star.Attr(r, n, builtins, properties) }
func (r *Module) AttrNames() []string                   { return star.AttrNames(builtins, properties) }

var builtins = map[string]*starlark.Builtin{
	"execute_activity": starlark.NewBuiltin("execute_activity", _executeActivity),
	"execute_workflow": starlark.NewBuiltin("execute_workflow", _executeWorkflow),
}

var properties = map[string]star.PropertyFactory{
	"execution_id":     _executionID,
	"execution_run_id": _executionRunID,
}

func _executionID(receiver starlark.Value) (starlark.Value, error) {
	info := receiver.(*Module).info
	return starlark.String(info.ExecutionID()), nil
}

func _executionRunID(receiver starlark.Value) (starlark.Value, error) {
	info := receiver.(*Module).info
	return starlark.String(info.RunID()), nil
}

func _executeActivity(t *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	activityID := args[0].(starlark.String).GoString()
	activityArgs := sliceTuple(args[1:])
	var ctx = service.GetContext(t)
	var w = service.GetWorkflow(t)
	logger := w.GetLogger(ctx)
	var asBytes bool
	for _, kv := range kwargs {
		k := kv[0].(starlark.String)
		switch k {
		case "task_list":
			v := kv[1].(starlark.String).GoString()
			ctx = w.WithTaskList(ctx, v)
		case "as_bytes":
			asBytes = bool(kv[1].(starlark.Bool))
		case "headers":
			// TODO: [feature] execute activity with given headers (context propagator)
			err := w.NewCustomError(yarpcerrors.CodeUnimplemented.String())
			logger.Error("builtin-error", ext.ZapError(err)...)
			return nil, err
		default:
			err := w.NewCustomError(yarpcerrors.CodeInvalidArgument.String(), fmt.Sprintf("unsupported key: %v", k))
			logger.Error("builtin-error", ext.ZapError(err)...)
			return nil, err
		}
	}
	f := w.ExecuteActivity(ctx, activityID, activityArgs...)
	return executeFuture(ctx, w, f, asBytes)
}

func _executeWorkflow(t *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	workflowID := args[0].(starlark.String).GoString()
	workflowArgs := sliceTuple(args[1:])
	var ctx = service.GetContext(t)
	var w = service.GetWorkflow(t)
	logger := w.GetLogger(ctx)
	var asBytes bool
	for _, kv := range kwargs {
		k := kv[0].(starlark.String)
		switch k {
		case "domain":
			v := kv[1].(starlark.String).GoString()
			ctx = w.WithWorkflowDomain(ctx, v)
		case "task_list":
			v := kv[1].(starlark.String).GoString()
			ctx = w.WithWorkflowTaskList(ctx, v)
		case "as_bytes":
			asBytes = bool(kv[1].(starlark.Bool))
		case "headers":
			// TODO: [feature] execute workflow with given headers (context propagator)
			err := w.NewCustomError(yarpcerrors.CodeUnimplemented.String())
			logger.Error("builtin-error", ext.ZapError(err)...)
			return nil, err
		default:
			err := w.NewCustomError(yarpcerrors.CodeInvalidArgument.String(), fmt.Sprintf("unsupported key: %v", k))
			logger.Error("builtin-error", ext.ZapError(err)...)
			return nil, err
		}
	}
	f := w.ExecuteChildWorkflow(ctx, workflowID, workflowArgs...)
	return executeFuture(ctx, w, f, asBytes)
}

func executeFuture(
	ctx workflow.Context,
	w workflow.Workflow,
	future workflow.Future,
	asBytes bool,
) (starlark.Value, error) {
	var err error
	var resBytes []byte
	var resValue starlark.Value
	if asBytes {
		err = future.Get(ctx, &resBytes)
	} else {
		err = future.Get(ctx, &resValue)
	}
	if err != nil {
		w.GetLogger(ctx).Error("builtin-error", zap.Bool("asBytes", asBytes), zap.Error(err))
		return nil, err
	}
	if asBytes {
		return starlark.Bytes(resBytes), nil
	} else {
		return resValue, nil
	}
}

func sliceTuple(args starlark.Tuple) []any {
	res := make([]any, args.Len())
	star.Iterate(args, func(i int, el starlark.Value) {
		res[i] = el
	})
	return res
}
