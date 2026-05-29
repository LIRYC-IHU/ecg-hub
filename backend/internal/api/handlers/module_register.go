package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	pb "github.com/LIRYC-IHU/ecg-hub/proto/modulepb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// RegisterModuleRequest is the body for POST /internal/modules/register.
type RegisterModuleRequest struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// RegisterModuleHandler handles POST /internal/modules/register.
// Called by module containers on startup to self-register with the hub.
// The hub validates the address by calling GetCapabilities() before accepting.
func RegisterModuleHandler(
	repo *repository.ModuleEndpointRepository,
	grpcManager *module.GRPCClientManager,
) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req RegisterModuleRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid body"))
		}
		if req.Name == "" || req.Address == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("VALIDATION_ERROR", "name and address are required"))
		}

		// Validate by calling GetCapabilities on the module.
		ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
		defer cancel()

		conn, err := grpc.NewClient(req.Address,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16*1024*1024)),
		)
		if err != nil {
			return c.JSON(http.StatusBadGateway, mw.APIError("CONNECTION_FAILED", "cannot connect to "+req.Address+": "+err.Error()))
		}
		defer conn.Close()

		client := pb.NewModuleServiceClient(conn)
		caps, err := client.GetCapabilities(ctx, &pb.GetCapabilitiesRequest{})
		if err != nil {
			return c.JSON(http.StatusBadGateway, mw.APIError("CAPABILITIES_FAILED", "GetCapabilities failed: "+err.Error()))
		}

		// Persist in DB.
		if err := repo.Upsert(caps.Name, req.Address, caps.Version); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}

		// Hot-add to the live gRPC client manager if available.
		if grpcManager != nil {
			grpcManager.AddModule(module.RemoteModuleConfig{Name: caps.Name, Address: req.Address})
		}

		return c.JSON(http.StatusOK, map[string]any{
			"registered": true,
			"name":       caps.Name,
			"version":    caps.Version,
			"extensions": caps.AcceptedExtensions,
		})
	}
}

// DeregisterModuleHandler handles DELETE /internal/modules/:name.
func DeregisterModuleHandler(repo *repository.ModuleEndpointRepository, grpcManager *module.GRPCClientManager) echo.HandlerFunc {
	return func(c echo.Context) error {
		name := c.Param("name")
		if err := repo.Delete(name); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		if grpcManager != nil {
			grpcManager.RemoveModule(name)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// ListModuleEndpointsHandler handles GET /internal/modules.
func ListModuleEndpointsHandler(repo *repository.ModuleEndpointRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		endpoints, err := repo.ListAll()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		return c.JSON(http.StatusOK, map[string]any{"data": endpoints, "total": len(endpoints)})
	}
}
