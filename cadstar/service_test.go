package cadstar

import (
	"context"
	"fmt"
	"testing"

	"github.com/cadence-workflow/starlark-worker/star"
	"github.com/stretchr/testify/suite"
	"go.starlark.net/starlark"
	"go.uber.org/cadence/activity"
	"go.uber.org/cadence/worker"
	"go.uber.org/cadence/workflow"
	"go.uber.org/zap"
)

type TestPlugin struct{}

var _ IPlugin = (*TestPlugin)(nil)

const testPluginID = "testplugin"

func (t *TestPlugin) ID() string {
	return testPluginID
}

func (t *TestPlugin) Create(_ RunInfo) starlark.Value {
	return &TestModule{}
}

func (t *TestPlugin) Register(registry worker.Registry) {
	registry.RegisterActivityWithOptions(stringifyActivity, activity.RegisterOptions{Name: "stringify_activity"})
}

func stringifyActivityWrapper(t *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	ctx := GetContext(t)
	var res starlark.String
	if err := workflow.ExecuteActivity(ctx, "stringify_activity", args).Get(ctx, &res); err != nil {
		return nil, err
	}
	return res, nil
}

func stringifyActivity(ctx context.Context, args starlark.Tuple) (starlark.String, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("stringify_activity", zap.String("args", args.String()))
	return starlark.String(args.String()), nil
}

type TestModule struct{}

var _ starlark.HasAttrs = &TestModule{}

func (f *TestModule) String() string        { return testPluginID }
func (f *TestModule) Type() string          { return testPluginID }
func (f *TestModule) Freeze()               {}
func (f *TestModule) Truth() starlark.Bool  { return true }
func (f *TestModule) Hash() (uint32, error) { return 0, fmt.Errorf("no-hash") }
func (f *TestModule) Attr(n string) (starlark.Value, error) {
	return star.Attr(f, n, testBuiltins, properties)
}
func (f *TestModule) AttrNames() []string { return star.AttrNames(testBuiltins, properties) }

var properties = map[string]star.PropertyFactory{}

var testBuiltins = map[string]*starlark.Builtin{
	"stringify_activity": starlark.NewBuiltin("stringify_activity", stringifyActivityWrapper),
}

type Test struct {
	suite.Suite
	StarTestSuite
	env *StarTestEnvironment
}

func TestSuite(t *testing.T) { suite.Run(t, new(Test)) }

func (r *Test) SetupSuite() {}

func (r *Test) SetupTest() {
	tp := &TestPlugin{}
	r.env = r.NewEnvironment(r.T(), &StarTestEnvironmentParams{
		RootDirectory: "testdata",
		Plugins:       map[string]IPlugin{tp.ID(): tp},
	})
}

func (r *Test) TearDownTest() { r.env.AssertExpectations(r.T()) }

func (r *Test) Test1() {
	r.env.ExecuteFunction("/app.star", "plus", starlark.Tuple{
		starlark.MakeInt(2),
		starlark.MakeInt(3),
	}, nil, nil)

	require := r.Require()
	var res starlark.Int
	require.NoError(r.env.GetResult(&res))
	require.Equal(starlark.MakeInt(5), res)
}

func (r *Test) TestPluginFunction() {
	r.env.ExecuteFunction("/app.star", "stringify", starlark.Tuple{
		starlark.String("foo"),
		starlark.MakeInt(100),
	}, nil, nil)

	require := r.Require()
	var res starlark.String
	require.NoError(r.env.GetResult(&res))
	require.Equal(starlark.String(`("foo", 100)`), res)
}
