package main

import (
	"context"
	"fmt"

	"github.com/antonmedv/expr"

	"github.com/crossplane/crossplane-runtime/pkg/errors"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"k8s.io/apimachinery/pkg/util/yaml"

	fnv1beta1 "github.com/crossplane/function-sdk-go/proto/v1beta1"
	"github.com/crossplane/function-sdk-go/request"
	"github.com/crossplane/function-sdk-go/resource"
	"github.com/crossplane/function-sdk-go/resource/composed"
	"github.com/crossplane/function-sdk-go/response"

	"github.com/crossplane/function-if/input/v1beta1"
)

// Function returns whatever response you ask it to.
type Function struct {
	fnv1beta1.UnimplementedFunctionRunnerServiceServer

	log logging.Logger
}

// RunFunction runs the Function.
func (f *Function) RunFunction(_ context.Context, req *fnv1beta1.RunFunctionRequest) (*fnv1beta1.RunFunctionResponse, error) {
	f.log.Info("Running Function", "tag", req.GetMeta().GetTag())

	// This creates a new response to the supplied request. Note that Functions
	// are run in a pipeline! Other Functions may have run before this one. If
	// they did, response.To will copy their desired state from req to rsp. Be
	// sure to pass through any desired state your Function is not concerned
	// with unmodified.
	rsp := response.To(req, response.DefaultTTL)

	// Input is supplied by the author of a Composition when they choose to run
	// your Function. Input is arbitrary, except that it must be a KRM-like
	// object. Supporting input is also optional - if you don't need to you can
	// delete this, and delete the input directory.
	in := &v1beta1.Input{}
	if err := request.GetInput(req, in); err != nil {
		response.Fatal(rsp, errors.Wrapf(err, "cannot get Function input from %T", req))
		return rsp, nil
	}

	// Get the observed composite resource (XR) from the request. There should
	// always be an observed XR in the request - this represents the current
	// state of the XR.
	oxr, err := request.GetObservedCompositeResource(req)
	if err != nil {
		response.Fatal(rsp, errors.Wrapf(err, "cannot get observed XR from %T", req))
		return rsp, nil
	}
	condition, err := expr.Compile(in.Condition.Expr, expr.Env(oxr.Resource.Object), expr.AsBool())
	if err != nil {
		response.Fatal(rsp, errors.Wrap(err, "condition has bad expression"))
		return rsp, nil
	}
	result, err := expr.Run(condition, oxr.Resource.Object)
	if err != nil {
		response.Fatal(rsp, errors.Wrap(err, "expression error"))
		return rsp, nil
	}
	if r, ok := result.(bool); !ok {
		response.Fatal(rsp, fmt.Errorf("%#v is not a Boolean", result))
		return rsp, nil
	} else if !r { // if did not succeed, return resource unmodified
		return rsp, nil
	}
	// Get any existing desired composed resources from the request.
	// Desired composed resources would exist if a previous Function in the
	// pipeline added them.
	desired, err := request.GetDesiredComposedResources(req)
	if err != nil {
		response.Fatal(rsp, errors.Wrapf(err, "cannot get desired composed resources from %T", req))
		return rsp, nil
	}
	for i, r := range in.Resources {
		var name string
		if r.Name == nil {
			name = fmt.Sprintf("resource-%d", i)
		} else {
			name = *r.Name
		}
		composed := composed.New()
		if err := yaml.Unmarshal(r.Base.Raw, composed); err != nil {
			response.Fatal(rsp, errors.Wrapf(err, "cannot get desired compose resource %q", *r.Name))
			return rsp, nil
		}
		desired[resource.Name(name)] = &resource.DesiredComposed{Resource: composed}
	}
	for _, r := range desired {
		r.Resource.SetLabels(map[string]string{"coolness": "superb"})
	}
	// Set our updated desired composed resource in the response we'll return.
	if err := response.SetDesiredComposedResources(rsp, desired); err != nil {
		response.Fatal(rsp, errors.Wrapf(err, "cannot set desired composed resources in %T", rsp))
		return rsp, nil
	}

	return rsp, nil
}
