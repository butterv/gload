package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/jhump/protoreflect/dynamic"

	"github.com/jhump/protoreflect/desc"

	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc/metadata"

	"github.com/jhump/protoreflect/dynamic/grpcdynamic"
	"google.golang.org/grpc"
	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
)

var errNoMethodNameSpecified = errors.New("no method name specified")
var base64Codecs = []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding}

func main() {
	ctx := context.Background()

	// TODO(butterv): Get connections for the number of workers.
	conn, err := grpc.DialContext(ctx, "localhost:8080", grpc.WithInsecure())
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	stub := grpcdynamic.NewStub(conn)

	var opts []grpc.CallOption
	//if w.config.enableCompression {
	// opts = append(opts, grpc.UseCompressor(gzip.Name))
	//}

	md := MetadataFromHeaders(nil)

	refCtx := metadata.NewOutgoingContext(ctx, md)
	refClient := grpcreflect.NewClient(refCtx, reflectpb.NewServerReflectionClient(conn))

	mtd, err := GetMethodDescFromReflect("grpc.health.v1.Health/Check", refClient)
	if err != nil {
		panic(err)
	}

	mdt := mtd.GetInputType()
	payloadMessage := dynamic.NewMessage(mdt)
	if payloadMessage == nil {
		panic(fmt.Errorf("no input type of method: %s", mtd.GetName()))
	}

	res, err := stub.InvokeRpc(ctx, mtd, payloadMessage, opts...)
	if err != nil {
		panic(err)
	}

	fmt.Printf("res: %s\n", res.String())
}

func MetadataFromHeaders(headers []string) metadata.MD {
	md := make(metadata.MD)
	for _, part := range headers {
		if part != "" {
			pieces := strings.SplitN(part, ":", 2)
			if len(pieces) == 1 {
				pieces = append(pieces, "") // if no value was specified, just make it "" (maybe the header value doesn't matter)
			}
			headerName := strings.ToLower(strings.TrimSpace(pieces[0]))
			val := strings.TrimSpace(pieces[1])
			if strings.HasSuffix(headerName, "-bin") {
				if v, err := decode(val); err == nil {
					val = v
				}
			}
			md[headerName] = append(md[headerName], val)
		}
	}
	return md
}

func decode(val string) (string, error) {
	var firstErr error
	var b []byte
	// we are lenient and can accept any of the flavors of base64 encoding
	for _, d := range base64Codecs {
		var err error
		b, err = d.DecodeString(val)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		return string(b), nil
	}
	return "", firstErr
}

// GetMethodDescFromReflect gets method descriptor for the call from reflection using client
func GetMethodDescFromReflect(call string, client *grpcreflect.Client) (*desc.MethodDescriptor, error) {
	call = strings.Replace(call, "/", ".", -1)
	file, err := client.FileContainingSymbol(call)
	if err != nil || file == nil {
		return nil, err
	}

	files := map[string]*desc.FileDescriptor{}
	files[file.GetName()] = file

	return getMethodDesc(call, files)
}

func getMethodDesc(call string, files map[string]*desc.FileDescriptor) (*desc.MethodDescriptor, error) {
	svc, mth, err := parseServiceMethod(call)
	if err != nil {
		return nil, err
	}

	dsc, err := findServiceSymbol(files, svc)
	if err != nil {
		return nil, err
	}
	if dsc == nil {
		return nil, fmt.Errorf("cannot find service %q", svc)
	}

	sd, ok := dsc.(*desc.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("cannot find service %q", svc)
	}

	mtd := sd.FindMethodByName(mth)
	if mtd == nil {
		return nil, fmt.Errorf("service %q does not include a method named %q", svc, mth)
	}

	return mtd, nil
}

func parseServiceMethod(svcAndMethod string) (string, string, error) {
	if len(svcAndMethod) == 0 {
		return "", "", errNoMethodNameSpecified
	}
	if svcAndMethod[0] == '.' {
		svcAndMethod = svcAndMethod[1:]
	}
	if len(svcAndMethod) == 0 {
		return "", "", errNoMethodNameSpecified
	}
	switch strings.Count(svcAndMethod, "/") {
	case 0:
		pos := strings.LastIndex(svcAndMethod, ".")
		if pos < 0 {
			return "", "", newInvalidMethodNameError(svcAndMethod)
		}
		return svcAndMethod[:pos], svcAndMethod[pos+1:], nil
	case 1:
		split := strings.Split(svcAndMethod, "/")
		return split[0], split[1], nil
	default:
		return "", "", newInvalidMethodNameError(svcAndMethod)
	}
}

func newInvalidMethodNameError(svcAndMethod string) error {
	return fmt.Errorf("method name must be package.Service.Method or package.Service/Method: %q", svcAndMethod)
}

func findServiceSymbol(resolved map[string]*desc.FileDescriptor, fullyQualifiedName string) (desc.Descriptor, error) {
	for _, fd := range resolved {
		if dsc := fd.FindSymbol(fullyQualifiedName); dsc != nil {
			return dsc, nil
		}
	}
	return nil, fmt.Errorf("cannot find service %q", fullyQualifiedName)
}
