package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jessevdk/go-flags"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/dynamic/grpcdynamic"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
)

var (
	base64Codecs = []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding}

	errNoMethodNameSpecified = errors.New("no method name specified")
)

type Options struct {
	Insecure          bool   `long:"insecure" description:"Use insecure connection."`
	Timeout           uint   `long:"timeout" description:"Timeout for each request. Default is 10s, use 0 for infinite." default:"10"`
	ConnectionTimeout uint   `long:"connection-timeout" description:"Connection timeout for the initial connection dial." default:"10"`
	Host              string `long:"host" description:"Host to be load tested." required:"true"`
	Concurrency       uint   `long:"concurrency" description:"Number of concurrent operations." default:"1"`
	Connections       int    `long:"connections" description:"Number of connections." default:"1"`
	Rps               uint   `long:"rps" description:"Request per seconds." default:"1"`
	Duration          uint   `long:"duration" description:"Duration(seconds) of the load test." required:"true"`
}

var opts Options

func main() {
	// TODO(butter): If panic, all connections are closed explicitly with using defer.

	parser := flags.NewParser(&opts, flags.Default)
	parser.Name = "gload"
	parser.Usage = "hogehoge"

	if _, err := parser.Parse(); err != nil {
		switch flagsErr := err.(type) {
		case *flags.Error:
			if flagsErr.Type == flags.ErrHelp {
				os.Exit(0)
			}
			os.Exit(1)
		default:
			os.Exit(1)
		}
	}

	ctx := context.Background()

	conns, err := newClientConnections(&opts)
	if err != nil {
		panic(err)
	}

	//var stubs []grpcdynamic.Stub
	//for i := 0; i < opt.connections; i++ {
	//	stub := grpcdynamic.NewStub(conns[i])
	//	stubs = append(stubs, stub)
	//}

	// TODO(butterv): The following are executed in parallel for each request.
	var opts []grpc.CallOption
	//if w.config.enableCompression {
	// opts = append(opts, grpc.UseCompressor(gzip.Name))
	//}

	for _, conn := range conns {
		md := metadataFromHeaders(nil)
		refCtx := metadata.NewOutgoingContext(ctx, md)
		refClient := grpcreflect.NewClient(refCtx, reflectpb.NewServerReflectionClient(conn))

		mtd, err := getMethodDescFromReflect("grpc.health.v1.Health/Check", refClient)
		if err != nil {
			panic(err)
		}

		mdt := mtd.GetInputType()
		payloadMessage := dynamic.NewMessage(mdt)
		if payloadMessage == nil {
			panic(fmt.Errorf("no input type of method: %s", mtd.GetName()))
		}

		stub := grpcdynamic.NewStub(conn)
		res, err := stub.InvokeRpc(ctx, mtd, payloadMessage, opts...)
		if err != nil {
			panic(err)
		}

		fmt.Printf("res: %s\n", res.String())
	}
}

func newClientConnections(opts *Options) ([]*grpc.ClientConn, error) {
	dialOptions := []grpc.DialOption{}

	if opts != nil {
		if opts.Insecure {
			dialOptions = append(dialOptions, grpc.WithInsecure())
		} else {
			// opts = append(opts, grpc.WithTransportCredentials(b.config.creds))
		}

		//if b.config.keepaliveTime > 0 {
		//	opts = append(opts, grpc.WithKeepaliveParams(keepalive.ClientParameters{
		//		Time:    b.config.keepaliveTime,
		//		Timeout: b.config.keepaliveTime,
		//	}))
		//}
	}

	var conns []*grpc.ClientConn
	for i := 0; i < opts.Connections; i++ {
		ctx := context.Background()
		//ctx, _ = context.WithTimeout(ctx, b.config.dialTimeout)

		conn, err := grpc.DialContext(ctx, opts.Host, dialOptions...)
		if err != nil {
			// TODO(butterv): If returns error, all connections are closed explicitly.
			return nil, err
		}

		conns = append(conns, conn)
	}

	return conns, nil
}

func metadataFromHeaders(headers []string) metadata.MD {
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
	//var b []byte

	// we are lenient and can accept any of the flavors of base64 encoding
	for _, d := range base64Codecs {
		//var err error
		b, err := d.DecodeString(val)
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

// getMethodDescFromReflect gets method descriptor for the call from reflection using client
func getMethodDescFromReflect(call string, client *grpcreflect.Client) (*desc.MethodDescriptor, error) {
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
