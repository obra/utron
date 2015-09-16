package utron

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/gernest/ita"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
)

var (

	//ErrRouteStringFormat is returned when the route string is of wrong format
	ErrRouteStringFormat = errors.New("wrong route string, example is\" get,post;/hello/world;Hello\"")
)

// Router registers routes and handlers. It embeds gorilla mux Router.
type Router struct {
	*mux.Router
	app *App
}

// NewRouter returns a new Router, if app is passed then it is used
func NewRouter(app ...*App) *Router {
	var dApp *App
	if len(app) > 0 {
		dApp = app[0]
	}
	return &Router{
		Router: mux.NewRouter(),
		app:    dApp,
	}
}

// route tracks information about  http route.
type route struct {
	pattern string   // url pattern e.g /home
	methods []string // http methds e.g GET, POST etc
	fn      string   // the name of the controller's method to be executed
}

// Add registers ctrl. It takes additional coma separated list of middlewares. middlewaes
// are of type
//	func(http.Handler)http.Handler
// 	or
// 	func(*Context)error
//
// utron uses alice package to chain middlewares, this means all alice compatible middlewares
// works out of the box.
func (r *Router) Add(ctrl Controller, middlewares ...interface{}) error {
	var (

		// routes is a slice of all routes associated
		// with ctrl
		routes []*route

		// baseController is the name of the Struct BaseController
		// when users embed the BaseController, an annonymous field
		// BaseController is added, and here we are refering to the name of the
		// annonymous field
		baseController = "BaseController"

		// routePaths is  the name of the field that allows uses to add Routes infromation
		routePaths = "Routes"
	)

	baseCtr := reflect.ValueOf(&BaseController{})
	ctrlVal := reflect.ValueOf(ctrl)

	bTyp := baseCtr.Type()
	cTyp := ctrlVal.Type()

	numCtr := cTyp.NumMethod()

	for v := range make([]struct{}, numCtr) {
		method := cTyp.Method(v)

		// skip methods defined by the base controller
		if _, bok := bTyp.MethodByName(method.Name); bok {
			continue
		}

		// patt composes pattern. This can be overidden by routes defined in the Routes
		// field of the controller.
		// By default the path is in the form /:contoller/:method. All http methods will be ristered
		// for this pattern, meaning it is up to the user to filter out what he/she wants, the easier way
		// is to use the Routes field instead
		//
		// TDDO: figure out the way of passing parameters to the method arguments?
		patt := "/" + strings.ToLower(getTypName(cTyp)) + "/" + strings.ToLower(method.Name)

		r := &route{
			pattern: patt,
			fn:      method.Name,
		}
		routes = append(routes, r)
	}

	// ultimate returns the actual value stored in rVals this means if rVals is a pointer,
	// the we return the value that is ponted to, we are dealing with structs, so the returned
	// value is of kind reflect.Struct.
	ultimate := func(rVals reflect.Value) reflect.Value {
		val := rVals
		switch val.Kind() {
		case reflect.Ptr:
			val = val.Elem()
		}
		return val
	}

	uCtr := ultimate(ctrlVal) // actual value after dereferencing the pointer.

	uCtrTyp := uCtr.Type() // we store the type, so we can use in the next iterations.

	for k := range make([]struct{}, uCtr.NumField()) {
		// We iterate in all fields, to filter out the user defined methods. We are aware
		// of methods inherited fron the BaseController( Since we recommend user Controllers
		// should embed BaseController).

		field := uCtrTyp.Field(k)

		// if we find any field matching BaseController
		// we initialize its value.
		if field.Name == baseController {
			fieldVal := uCtr.Field(k)
			fieldVal.Set(reflect.ValueOf(new(BaseController)))
			continue
		}

		// if there is any field named Routes, and it is of signature []string
		// then  the field's value is used to overide the patterns  defined earlier.
		//
		// It is not necessary for every user implementation to define method named Routes
		// If we cant find it then we just ignore its use( fallback to defaults).
		//
		// Route strings, are of the form "httpMethods;path;method"
		// where httMethod: is a coma separated http method strings
		//                  e.g GET,POST,PUT.
		//                  The case does not matter, you can use lower case or upper case charaters
		//                  or even mixed case, that is get,GET,gET and GeT will all be treated as GET
		//
		//        path:     Is a url path or pattern, utron uses gorilla mux package. So, everything you can do
		//                  with gorilla mux url path then you can do here.
		//                  e.g /hello/{world}
		//                  Don't worry about the params, they will be accessibe via .Ctx.Params field in your
		//                  controller.
		//
		//        method:   The name of the user Controller method to execute for this route.
		if field.Name == routePaths {
			fieldVal := uCtr.Field(k)
			switch fieldVal.Kind() {
			case reflect.Slice:
				if data, ok := fieldVal.Interface().([]string); ok {
					for _, d := range data {
						rt, err := splitRoutes(d)
						if err != nil {
							continue
						}
						for key := range routes {
							value := routes[key]
							if value.fn == rt.fn {
								if rt.methods != nil {
									value.methods = rt.methods
								}
								if rt.pattern != "" {
									value.pattern = rt.pattern
								}
							}
						}
					}

				}
			}
		}

	}

	for _, v := range routes {
		if err := r.add(v, ctrl, middlewares...); err != nil {
			return err
		}
	}
	return nil
}

// getTypName returns a string representing the name of the object typ.
// if the name is defined then  it is used, otherwise, the  name is derived from the
// Stringer interface.
//
// the stringer retuns something like *somepkg.MyStruct, so skip
// the *somepkg and return MyStruct
func getTypName(typ reflect.Type) string {
	if typ.Name() != "" {
		return typ.Name()
	}
	split := strings.Split(typ.String(), ".")
	return split[len(split)-1]
}

// splitRoutes harvest the route components from srouteStr.
func splitRoutes(routeStr string) (*route, error) {

	// supported contains supported http methods
	supported := "GET POST PUT PATCH TRACE PATCH DELETE HEAD OPTIONS"

	// separator is a character used to separate route components from the routes string
	separator := ";"

	activeRoute := &route{}
	if routeStr != "" {
		s := strings.Split(routeStr, separator)
		if len(s) != 3 {
			return nil, ErrRouteStringFormat
		}

		m := strings.Split(s[0], ",")
		for _, v := range m {
			up := strings.ToUpper(v)
			if !strings.Contains(supported, up) {
				return nil, ErrRouteStringFormat
			}
			activeRoute.methods = append(activeRoute.methods, up)
		}
		p := s[1]
		if !strings.Contains(p, "/") {
			return nil, ErrRouteStringFormat
		}
		activeRoute.pattern = p
		activeRoute.fn = s[2]
		return activeRoute, nil

	}
	return nil, ErrRouteStringFormat
}

// add registers controller ctrl, using activeRoute. If middlewares are provided, utron uses
// alice package to chain middlewares.
func (r *Router) add(activeRoute *route, ctrl Controller, middlewares ...interface{}) error {
	chain := alice.New() // alice on chains
	if len(middlewares) > 0 {
		var m []alice.Constructor
		for _, v := range middlewares {
			switch v.(type) {
			case func(http.Handler) http.Handler:
				m = append(m, v.(func(http.Handler) http.Handler))
			case func(*Context) error:

				// wrap func(*Context)error to a func(http.Handler)http.Handler
				//
				//TODO put this into a separate funcion?
				ctxMiddleware := func(h http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
						ctx := NewContext(w, req)
						r.prepareContext(ctx)
						err := v.(func(*Context) error)(ctx)
						if err != nil {
							cerr := ctx.Commit()
							if cerr != nil {
								logThis.Errors(req.URL.Path, " ", cerr.Error())
							}
							return
						}
						h.ServeHTTP(ctx.Response(), ctx.Request())
					})
				}

				m = append(m, ctxMiddleware)

			default:
				return fmt.Errorf("unsupported middleware %v", v)
			}
		}
		chain = alice.New(m...)
	}

	// register methods if any
	if len(activeRoute.methods) > 0 {
		r.HandleFunc(activeRoute.pattern, func(w http.ResponseWriter, req *http.Request) {
			chain.ThenFunc(r.wrapController(activeRoute.fn, ctrl)).ServeHTTP(w, req)
		}).Methods(activeRoute.methods...)
		return nil
	}

	r.HandleFunc(activeRoute.pattern, func(w http.ResponseWriter, req *http.Request) {
		chain.ThenFunc(r.wrapController(activeRoute.fn, ctrl)).ServeHTTP(w, req)
	})

	return nil
}

// prepareContext sets view,config and model on the ctx.
func (r *Router) prepareContext(ctx *Context) {
	if r.app != nil {
		if r.app.view != nil {
			ctx.Set(r.app.view)
		}
		if r.app.cfg != nil {
			ctx.Cfg = r.app.cfg
		}
		if r.app.model != nil {
			ctx.DB = r.app.model
		}
	}
}

// executes the method fn on Controller ctrl, it sets conext.
func (r *Router) handleController(w http.ResponseWriter, req *http.Request, fn string, ctrl Controller) {
	ctx := NewContext(w, req)
	r.prepareContext(ctx)
	ctrl.New(ctx)

	// execute the method
	// TDOO: better error handling?
	if x := ita.New(ctrl).Call(fn); x.Error() != nil {
		ctx.Set(http.StatusInternalServerError)
		ctx.Write([]byte(x.Error().Error()))
		ctx.TextPlain()
		ctx.Commit()
		return
	}
	err := ctrl.Render()
	if err != nil {
		logThis.Errors(err)
	}
}

// wrapController wraps a controller ctrl with method fn, and returns http.HandleFunc
func (r *Router) wrapController(fn string, ctrl Controller) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		r.handleController(w, req, fn, ctrl)
	}
}