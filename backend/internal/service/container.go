package service

import "cascade-demoops/backend/internal/repository"

type Container struct {
	Projects *ProjectService
}

func NewContainer(repos repository.Repositories) Container {
	return Container{
		Projects: NewProjectService(repos.Projects, repos.Contexts, repos.Audit),
	}
}