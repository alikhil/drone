// Copyright 2022 Harness Inc. All rights reserved.
// Use of this source code is governed by the Polyform Free Trial License
// that can be found in the LICENSE.md file for this repository.

package execution

import (
	"encoding/json"
	"net/http"

	"github.com/harness/gitness/internal/api/controller/execution"
	"github.com/harness/gitness/internal/api/render"
	"github.com/harness/gitness/internal/api/request"
	"github.com/harness/gitness/internal/paths"
)

func HandleUpdate(executionCtrl *execution.Controller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		session, _ := request.AuthSessionFrom(ctx)

		in := new(execution.UpdateInput)
		err := json.NewDecoder(r.Body).Decode(in)
		if err != nil {
			render.BadRequestf(w, "Invalid Request Body: %s.", err)
			return
		}

		pipelineRef, err := request.GetPipelineRefFromPath(r)
		if err != nil {
			render.TranslatedUserError(w, err)
			return
		}
		spaceRef, pipelineUID, err := paths.DisectLeaf(pipelineRef)
		if err != nil {
			render.TranslatedUserError(w, err)
			return
		}
		n, err := request.GetExecutionNumberFromPath(r)
		if err != nil {
			render.TranslatedUserError(w, err)
			return
		}

		pipeline, err := executionCtrl.Update(ctx, session, spaceRef, pipelineUID, n, in)
		if err != nil {
			render.TranslatedUserError(w, err)
			return
		}

		render.JSON(w, http.StatusOK, pipeline)
	}
}
