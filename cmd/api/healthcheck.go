package main

import (
	"net/http"
)

// healthcheckHandler is a simple endpoint that returns a 200 OK response with a JSON body indicating
// the service is available. It can be used by load balancers or monitoring systems to check the health of the application.
func (app *application) healthcheckHandler(w http.ResponseWriter, r *http.Request) {
	env := envelope{
		"status": "available",
		"system_info": map[string]string{
			"environment": app.config.env,
		},
	}

	err := app.writeJSON(w, http.StatusOK, env, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
